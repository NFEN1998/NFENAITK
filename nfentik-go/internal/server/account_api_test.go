package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/nfentik/nfentik-go/internal/store"
)

type accountClient struct {
	t      *testing.T
	base   string
	client *http.Client
	jar    http.CookieJar
}

func newAccountClient(t *testing.T, ts *httptest.Server) *accountClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &accountClient{t: t, base: ts.URL, client: &http.Client{Jar: jar}, jar: jar}
}

func (c *accountClient) do(method, path string, body any, headers map[string]string) (int, map[string]any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.client.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	var payload map[string]any
	_ = json.NewDecoder(res.Body).Decode(&payload)
	return res.StatusCode, payload
}

func (c *accountClient) csrf() string {
	c.t.Helper()
	u, err := url.Parse(c.base)
	if err != nil {
		c.t.Fatalf("parse base: %v", err)
	}
	for _, cookie := range c.jar.Cookies(u) {
		if cookie.Name == csrfCookieName {
			return cookie.Value
		}
	}
	return ""
}

func (c *accountClient) csrfHeaders() map[string]string {
	return map[string]string{"X-CSRF-Token": c.csrf()}
}

func TestAccountRegisterLoginAndSession(t *testing.T) {
	_, ts := testServer(t)
	client := newAccountClient(t, ts)

	code, payload := client.do(http.MethodPost, "/api/user/register", map[string]any{
		"username": "portal_user", "password": "password123",
	}, nil)
	if code != http.StatusOK || payload["success"] != true {
		t.Fatalf("register: %d %v", code, payload)
	}
	user, _ := payload["user"].(map[string]any)
	if user == nil || user["token"] == "" {
		t.Fatalf("register did not return a token: %v", payload)
	}

	code, payload = client.do(http.MethodGet, "/api/user/me", nil, nil)
	if code != http.StatusOK || payload["success"] != true {
		t.Fatalf("session /me: %d %v", code, payload)
	}

	code, payload = client.do(http.MethodPost, "/api/user/account/logout", nil, client.csrfHeaders())
	if code != http.StatusOK || payload["success"] != true {
		t.Fatalf("logout: %d %v", code, payload)
	}
	code, _ = client.do(http.MethodGet, "/api/user/me", nil, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("/me after logout = %d, want 401", code)
	}

	code, payload = client.do(http.MethodPost, "/api/user/account/login", map[string]any{
		"username": "portal_user", "password": "password123",
	}, nil)
	if code != http.StatusOK || payload["success"] != true {
		t.Fatalf("account login: %d %v", code, payload)
	}
	code, payload = client.do(http.MethodGet, "/api/user/account/me", nil, nil)
	if code != http.StatusOK || payload["account"] == nil {
		t.Fatalf("account /me: %d %v", code, payload)
	}

	code, payload = client.do(http.MethodPost, "/api/user/account/login", map[string]any{
		"username": "portal_user", "password": "wrong-password",
	}, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d %v", code, payload)
	}
}

func TestAccountBindExistingToken(t *testing.T) {
	srv, ts := testServer(t)
	remain := int64(3)
	user, err := srv.store.CreateUser(store.User{
		PlanType: "count", PlanCode: "count", PlanLabel: "按次",
		TotalCount: &remain, RemainCount: &remain, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	client := newAccountClient(t, ts)
	code, payload := client.do(http.MethodPost, "/api/user/account/bind", map[string]any{
		"username": "bound_user", "password": "password123",
	}, map[string]string{"Authorization": "Bearer " + user.Token})
	if code != http.StatusOK || payload["success"] != true {
		t.Fatalf("bind: %d %v", code, payload)
	}
	account, _ := payload["account"].(map[string]any)
	if account == nil || account["username"] != "bound_user" {
		t.Fatalf("bind account payload: %v", payload)
	}

	code, payload = client.do(http.MethodPost, "/api/user/account/bind", map[string]any{
		"username": "second_user", "password": "password123",
	}, map[string]string{"Authorization": "Bearer " + user.Token})
	if code != http.StatusConflict {
		t.Fatalf("second bind = %d %v, want 409", code, payload)
	}

	code, payload = client.do(http.MethodGet, "/api/user/account/me", nil, map[string]string{
		"Authorization": "Bearer " + user.Token,
	})
	if code != http.StatusOK || payload["account"] == nil {
		t.Fatalf("token account /me: %d %v", code, payload)
	}
}

func TestRedeemUpdatesQuota(t *testing.T) {
	srv, ts := testServer(t)
	client := newAccountClient(t, ts)
	code, payload := client.do(http.MethodPost, "/api/user/register", map[string]any{
		"username": "redeem_user", "password": "password123",
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("register: %d %v", code, payload)
	}
	user, _ := payload["user"].(map[string]any)
	userID := int64(user["id"].(float64))

	_, codes, err := srv.store.CreateRedemptionBatch("count", "count", 5, 0, 1, "", "")
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	code, payload = client.do(http.MethodPost, "/api/user/redeem", map[string]any{"code": codes[0]}, client.csrfHeaders())
	if code != http.StatusOK || payload["success"] != true {
		t.Fatalf("redeem: %d %v", code, payload)
	}
	updated, err := srv.store.GetUserByID(userID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if updated.RemainCount == nil || *updated.RemainCount != 5 {
		t.Fatalf("remain = %v, want 5", updated.RemainCount)
	}

	code, payload = client.do(http.MethodPost, "/api/user/redeem", map[string]any{"code": codes[0]}, client.csrfHeaders())
	if code != http.StatusBadRequest {
		t.Fatalf("second redeem = %d %v, want 400", code, payload)
	}
}

func TestAccountPasswordChange(t *testing.T) {
	_, ts := testServer(t)
	client := newAccountClient(t, ts)
	code, payload := client.do(http.MethodPost, "/api/user/register", map[string]any{
		"username": "password_user", "password": "password123",
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("register: %d %v", code, payload)
	}
	code, payload = client.do(http.MethodPost, "/api/user/account/password", map[string]any{
		"old_password": "password123", "new_password": "newpassword123",
	}, client.csrfHeaders())
	if code != http.StatusOK || payload["success"] != true {
		t.Fatalf("change password: %d %v", code, payload)
	}
	code, payload = client.do(http.MethodPost, "/api/user/account/login", map[string]any{
		"username": "password_user", "password": "password123",
	}, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("old password login = %d %v, want 401", code, payload)
	}
	code, payload = client.do(http.MethodPost, "/api/user/account/login", map[string]any{
		"username": "password_user", "password": "newpassword123",
	}, nil)
	if code != http.StatusOK || payload["success"] != true {
		t.Fatalf("new password login: %d %v", code, payload)
	}
}

func TestRegisterCanBeDisabled(t *testing.T) {
	srv, ts := testServer(t)
	if _, err := srv.config.Patch(json.RawMessage(`{"accountRegistrationEnabled":false}`)); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	client := newAccountClient(t, ts)
	code, payload := client.do(http.MethodPost, "/api/user/register", map[string]any{
		"username": "disabled_register", "password": "password123",
	}, nil)
	if code != http.StatusForbidden {
		t.Fatalf("register with disabled switch = %d %v, want 403", code, payload)
	}
	if _, err := srv.config.Patch(json.RawMessage(`{"accountRegistrationEnabled":true}`)); err != nil {
		t.Fatalf("restore settings: %v", err)
	}
}

func TestSessionWriteRequiresCSRF(t *testing.T) {
	_, ts := testServer(t)
	client := newAccountClient(t, ts)
	code, payload := client.do(http.MethodPost, "/api/user/register", map[string]any{
		"username": "csrf_user", "password": "password123",
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("register: %d %v", code, payload)
	}
	code, payload = client.do(http.MethodPost, "/api/user/account/password", map[string]any{
		"old_password": "password123", "new_password": "newpassword123",
	}, nil)
	if code != http.StatusForbidden {
		t.Fatalf("password change without CSRF = %d %v, want 403", code, payload)
	}
	code, payload = client.do(http.MethodPost, "/api/user/account/password", map[string]any{
		"old_password": "password123", "new_password": "newpassword123",
	}, client.csrfHeaders())
	if code != http.StatusOK {
		t.Fatalf("password change with CSRF = %d %v, want 200", code, payload)
	}
}

func TestAdminUnbindAndRebind(t *testing.T) {
	_, ts := testServer(t)
	client := newAccountClient(t, ts)
	code, payload := client.do(http.MethodPost, "/api/user/register", map[string]any{
		"username": "unbind_user", "password": "password123",
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("register: %d %v", code, payload)
	}
	account, _ := payload["account"].(map[string]any)
	user, _ := payload["user"].(map[string]any)
	accountID := int64(account["id"].(float64))
	token, _ := user["token"].(string)

	code, payload = client.do(http.MethodPost, "/api/admin/accounts/"+itoa(accountID)+"/unbind", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("unbind: %d %v", code, payload)
	}
	fresh := newAccountClient(t, ts)
	code, payload = fresh.do(http.MethodPost, "/api/user/account/login", map[string]any{
		"username": "unbind_user", "password": "password123",
	}, nil)
	if code != http.StatusConflict {
		t.Fatalf("unbound login = %d %v, want 409", code, payload)
	}

	code, payload = client.do(http.MethodPost, "/api/admin/accounts/"+itoa(accountID)+"/bind", map[string]any{
		"token": token,
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("rebind: %d %v", code, payload)
	}
	code, payload = fresh.do(http.MethodPost, "/api/user/account/login", map[string]any{
		"username": "unbind_user", "password": "password123",
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("login after rebind = %d %v, want 200", code, payload)
	}
}

func TestAdminAccountsAndRedemption(t *testing.T) {
	_, ts := testServer(t)
	client := newAccountClient(t, ts)

	code, payload := client.do(http.MethodPost, "/api/user/register", map[string]any{
		"username": "admin_managed", "password": "password123",
	}, nil)
	if code != http.StatusOK {
		t.Fatalf("register: %d %v", code, payload)
	}
	account, _ := payload["account"].(map[string]any)
	accountID := int64(account["id"].(float64))

	code, payload = client.do(http.MethodGet, "/api/admin/accounts?q=admin_managed", nil, nil)
	if code != http.StatusOK || payload["success"] != true {
		t.Fatalf("admin accounts: %d %v", code, payload)
	}
	accounts, _ := payload["accounts"].([]any)
	if len(accounts) != 1 {
		t.Fatalf("admin accounts = %v", payload["accounts"])
	}

	code, payload = client.do(http.MethodPost, "/api/admin/accounts/"+itoa(accountID)+"/disable", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("disable account: %d %v", code, payload)
	}
	code, _ = client.do(http.MethodGet, "/api/user/account/me", nil, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("disabled session /me = %d, want 401", code)
	}

	code, payload = client.do(http.MethodPost, "/api/admin/redeem/batches", map[string]any{
		"kind": "count", "plan_code": "count", "amount": 3, "quantity": 2,
	}, nil)
	if code != http.StatusOK || payload["success"] != true {
		t.Fatalf("create batch: %d %v", code, payload)
	}
	codes, _ := payload["codes"].([]any)
	if len(codes) != 2 {
		t.Fatalf("codes = %v", payload["codes"])
	}
	batch, _ := payload["batch"].(map[string]any)
	batchID := int64(batch["id"].(float64))
	code, payload = client.do(http.MethodPost, "/api/admin/redeem/batches/"+itoa(batchID)+"/revoke", nil, nil)
	if code != http.StatusOK || payload["revoked"].(float64) != 2 {
		t.Fatalf("revoke batch: %d %v", code, payload)
	}
}
