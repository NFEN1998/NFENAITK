package store

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ErrAccountNotFound is returned when no account matches the lookup.
var ErrAccountNotFound = errors.New("账号不存在")

// ErrAccountExists is returned when a username is already taken.
var ErrAccountExists = errors.New("用户名已被占用")

// ErrAccountBound is returned when the user already has an account.
var ErrAccountBound = errors.New("该令牌已绑定账号")

// ErrAccountNotBound is returned when an account has no token attached.
var ErrAccountNotBound = errors.New("账号未绑定令牌")

// Account is the web portal identity. It owns exactly one user/token.
type Account struct {
	ID           int64   `json:"id"`
	Username     string  `json:"username"`
	UsernameKey  string  `json:"-"`
	PasswordHash string  `json:"-"`
	UserID       *int64  `json:"user_id,omitempty"`
	Status       string  `json:"status"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	LastLoginAt  *string `json:"last_login_at,omitempty"`
}

// AccountView joins the account with its token and plan for the admin list.
type AccountView struct {
	Account
	Token       string  `json:"token"`
	PlanType    string  `json:"plan_type"`
	PlanCode    string  `json:"plan_code"`
	PlanLabel   string  `json:"plan_label"`
	ExpireAt    *string `json:"expire_at,omitempty"`
	TotalCount  *int64  `json:"total_count,omitempty"`
	UsedCount   int64   `json:"used_count"`
	RemainCount *int64  `json:"remain_count,omitempty"`
	UserEnabled bool    `json:"user_enabled"`
}

func accountKey(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func (s *Store) accountColumns() string {
	return `Id, Username, UsernameKey, PasswordHash, UserId, Status,
		` + s.dialect.formatTimestamp("CreatedAt") + `,
		` + s.dialect.formatTimestamp("UpdatedAt") + `,
		` + s.dialect.formatTimestamp("LastLoginAt")
}

func scanAccount(scan func(dest ...any) error) (Account, error) {
	var (
		a           Account
		userID      sql.NullInt64
		createdAt   sql.NullString
		updatedAt   sql.NullString
		lastLoginAt sql.NullString
	)
	if err := scan(&a.ID, &a.Username, &a.UsernameKey, &a.PasswordHash, &userID,
		&a.Status, &createdAt, &updatedAt, &lastLoginAt); err != nil {
		return Account{}, err
	}
	if userID.Valid {
		a.UserID = &userID.Int64
	}
	if createdAt.Valid {
		a.CreatedAt = createdAt.String
	}
	if updatedAt.Valid {
		a.UpdatedAt = updatedAt.String
	}
	if lastLoginAt.Valid {
		a.LastLoginAt = &lastLoginAt.String
	}
	return a, nil
}

// CreateAccountWithNewUser creates a zero-quota count user and binds a new
// account to it in one transaction.
func (s *Store) CreateAccountWithNewUser(username, passwordHash string) (Account, User, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Account{}, User{}, err
	}
	defer tx.Rollback()

	token, err := NewUserToken()
	if err != nil {
		return Account{}, User{}, err
	}
	zero := int64(0)
	userID, err := s.insertReturningIDTx(tx, `INSERT INTO Users
		(Token, PlanType, PlanCode, PlanLabel, StartAt, ExpireAt, TotalCount, UsedCount, RemainCount, Note, Enabled, CreatedAt, UpdatedAt)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW(),NOW())`,
		token, "count", "count", "按次", time.Now().Format("2006-01-02 15:04:05"),
		nil, zero, int64(0), zero, "", true)
	if err != nil {
		return Account{}, User{}, err
	}
	accountID, err := s.insertReturningIDTx(tx, `INSERT INTO Accounts
		(Username, UsernameKey, PasswordHash, UserId, Status, CreatedAt, UpdatedAt)
		VALUES ($1,$2,$3,$4,'enabled',NOW(),NOW())`,
		strings.TrimSpace(username), accountKey(username), passwordHash, userID)
	if err != nil {
		if isUniqueViolation(err) {
			return Account{}, User{}, ErrAccountExists
		}
		return Account{}, User{}, err
	}
	if err := tx.Commit(); err != nil {
		return Account{}, User{}, err
	}
	account, err := s.GetAccountByID(accountID)
	if err != nil {
		return Account{}, User{}, err
	}
	user, err := s.GetUserByID(userID)
	if err != nil {
		return Account{}, User{}, err
	}
	return account, user, nil
}

// CreateAccountForUser binds a new account to an existing user.
func (s *Store) CreateAccountForUser(username, passwordHash string, userID int64) (Account, error) {
	if _, err := s.GetAccountByUserID(userID); err == nil {
		return Account{}, ErrAccountBound
	} else if !errors.Is(err, ErrAccountNotFound) {
		return Account{}, err
	}
	if _, err := s.GetAccountByUsername(username); err == nil {
		return Account{}, ErrAccountExists
	} else if !errors.Is(err, ErrAccountNotFound) {
		return Account{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()

	accountID, err := s.insertReturningIDTx(tx, `INSERT INTO Accounts
		(Username, UsernameKey, PasswordHash, UserId, Status, CreatedAt, UpdatedAt)
		VALUES ($1,$2,$3,$4,'enabled',NOW(),NOW())`,
		strings.TrimSpace(username), accountKey(username), passwordHash, userID)
	if err != nil {
		if isUniqueViolation(err) {
			return Account{}, ErrAccountExists
		}
		return Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return Account{}, err
	}
	return s.GetAccountByID(accountID)
}

// GetAccountByID loads one account.
func (s *Store) GetAccountByID(id int64) (Account, error) {
	acc, err := scanAccount(s.queryRow(`SELECT `+s.accountColumns()+` FROM Accounts WHERE Id = $1`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	return acc, err
}

// GetAccountByUsername loads one account by its case-insensitive key.
func (s *Store) GetAccountByUsername(username string) (Account, error) {
	acc, err := scanAccount(s.queryRow(`SELECT `+s.accountColumns()+` FROM Accounts WHERE UsernameKey = $1`, accountKey(username)).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	return acc, err
}

// GetAccountByUserID loads the account bound to a user.
func (s *Store) GetAccountByUserID(userID int64) (Account, error) {
	acc, err := scanAccount(s.queryRow(`SELECT `+s.accountColumns()+` FROM Accounts WHERE UserId = $1`, userID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	return acc, err
}

// BindAccount attaches an unbound account to a user.
func (s *Store) BindAccount(accountID, userID int64) error {
	res, err := s.exec(`UPDATE Accounts SET UserId = $1, Status = 'enabled', UpdatedAt = NOW()
		WHERE Id = $2 AND UserId IS NULL`, userID, accountID)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAccountBound
		}
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrAccountBound
	}
	return nil
}

// UnbindAccount detaches an account from its user and disables login until it
// is bound again.
func (s *Store) UnbindAccount(accountID int64) error {
	_, err := s.exec(`UPDATE Accounts SET UserId = NULL, Status = 'unbound', UpdatedAt = NOW()
		WHERE Id = $1`, accountID)
	return err
}

// SetAccountPassword replaces the password digest.
func (s *Store) SetAccountPassword(accountID int64, passwordHash string) error {
	_, err := s.exec(`UPDATE Accounts SET PasswordHash = $1, UpdatedAt = NOW() WHERE Id = $2`,
		passwordHash, accountID)
	return err
}

// SetAccountStatus enables or disables an account.
func (s *Store) SetAccountStatus(accountID int64, status string) error {
	_, err := s.exec(`UPDATE Accounts SET Status = $1, UpdatedAt = NOW() WHERE Id = $2`,
		status, accountID)
	return err
}

// TouchAccountLogin records a successful login time.
func (s *Store) TouchAccountLogin(accountID int64) error {
	_, err := s.exec(`UPDATE Accounts SET LastLoginAt = NOW(), UpdatedAt = NOW() WHERE Id = $1`, accountID)
	return err
}

// DeleteAccount removes an account row. The bound user and token are kept.
func (s *Store) DeleteAccount(accountID int64) error {
	_, err := s.exec(`DELETE FROM Accounts WHERE Id = $1`, accountID)
	return err
}

// ListAccounts returns a page of accounts joined with their user plan.
func (s *Store) ListAccounts(search string, page, pageSize int) ([]AccountView, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	pattern := "%" + strings.TrimSpace(search) + "%"
	where := ""
	args := []any{}
	if strings.TrimSpace(search) != "" {
		where = ` WHERE ` + s.dialect.ilike("a.Username", "$1")
		args = append(args, pattern)
	}
	var total int64
	if err := s.queryRow(`SELECT COUNT(*) FROM Accounts a`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `SELECT a.Id, a.Username, a.UsernameKey, a.PasswordHash, a.UserId, a.Status,
		` + s.dialect.formatTimestamp("a.CreatedAt") + `,
		` + s.dialect.formatTimestamp("a.UpdatedAt") + `,
		` + s.dialect.formatTimestamp("a.LastLoginAt") + `,
		COALESCE(u.Token, ''), COALESCE(u.PlanType, ''), COALESCE(u.PlanCode, ''), COALESCE(u.PlanLabel, ''),
		u.ExpireAt, u.TotalCount, COALESCE(u.UsedCount, 0), u.RemainCount, COALESCE(u.Enabled, FALSE)
		FROM Accounts a LEFT JOIN Users u ON u.Id = a.UserId` + where + `
		ORDER BY a.Id DESC LIMIT $` + strconv.Itoa(len(args)+1) + ` OFFSET $` + strconv.Itoa(len(args)+2)
	args = append(args, pageSize, (page-1)*pageSize)

	rows, err := s.query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []AccountView{}
	for rows.Next() {
		var (
			v           AccountView
			userID      sql.NullInt64
			createdAt   sql.NullString
			updatedAt   sql.NullString
			lastLoginAt sql.NullString
			expireAt    sql.NullString
			total       sql.NullInt64
			remain      sql.NullInt64
			used        sql.NullInt64
			userEnabled sql.NullBool
		)
		if err := rows.Scan(&v.ID, &v.Username, &v.UsernameKey, &v.PasswordHash, &userID,
			&v.Status, &createdAt, &updatedAt, &lastLoginAt, &v.Token,
			&v.PlanType, &v.PlanCode, &v.PlanLabel, &expireAt, &total, &used, &remain, &userEnabled); err != nil {
			return nil, 0, err
		}
		if userID.Valid {
			v.UserID = &userID.Int64
		}
		if createdAt.Valid {
			v.CreatedAt = createdAt.String
		}
		if updatedAt.Valid {
			v.UpdatedAt = updatedAt.String
		}
		if lastLoginAt.Valid {
			v.LastLoginAt = &lastLoginAt.String
		}
		if expireAt.Valid {
			v.ExpireAt = &expireAt.String
		}
		if total.Valid {
			v.TotalCount = &total.Int64
		}
		if remain.Valid {
			v.RemainCount = &remain.Int64
		}
		v.UsedCount = used.Int64
		v.UserEnabled = userEnabled.Valid && userEnabled.Bool
		out = append(out, v)
	}
	return out, total, rows.Err()
}
