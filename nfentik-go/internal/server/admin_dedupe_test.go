package server

import (
	"net/http"
	"testing"
	"time"
)

// TestAdminDedupeEndpoints starts a background dedupe through the console API
// and polls the status endpoint until it settles.
func TestAdminDedupeEndpoints(t *testing.T) {
	_, ts := testServer(t)

	code, payload := doJSON(t, http.MethodPost, ts.URL+"/api/admin/questions/dedupe", nil, nil)
	if code != http.StatusOK || payload["success"] != true {
		t.Fatalf("start dedupe: %d %v", code, payload)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		code, payload = doJSON(t, http.MethodGet, ts.URL+"/api/admin/questions/dedupe", nil, nil)
		if code != http.StatusOK || payload["success"] != true {
			t.Fatalf("dedupe status: %d %v", code, payload)
		}
		status, _ := payload["status"].(map[string]any)
		if status == nil {
			t.Fatalf("missing status: %v", payload)
		}
		if running, _ := status["running"].(bool); !running {
			if msg, _ := status["error"].(string); msg != "" {
				t.Fatalf("dedupe error: %s", msg)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("dedupe did not finish in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
