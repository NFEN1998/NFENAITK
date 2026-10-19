package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nfentik/nfentik-go/internal/plan"
)

// Redemption code errors surfaced to the user portal.
var (
	ErrCodeInvalid          = errors.New("卡密无效或已使用")
	ErrCodeExpired          = errors.New("卡密已过期")
	ErrCodeNeedsDuration    = errors.New("当前套餐请使用时长卡密")
	ErrCodeNeedsCount       = errors.New("当前套餐请使用次数卡密")
	ErrCodeNoNeed           = errors.New("当前套餐无需充值")
	ErrCodeInvalidBatch     = errors.New("卡密批次参数无效")
	ErrCodeInvalidCodeInput = errors.New("卡密格式无效")
	ErrRedeemUserDisabled   = errors.New("账号已停用")
)

// RedemptionBatch is one generated set of cards.
type RedemptionBatch struct {
	ID        int64  `json:"id"`
	BatchNo   string `json:"batch_no"`
	Kind      string `json:"kind"`
	PlanCode  string `json:"plan_code"`
	Amount    int64  `json:"amount"`
	Days      int    `json:"days"`
	Quantity  int    `json:"quantity"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
	UsedCount int64  `json:"used_count"`
}

// RedemptionCode is one card. The plaintext is only returned when the batch is
// created; the database stores the digest.
type RedemptionCode struct {
	ID        int64   `json:"id"`
	BatchID   int64   `json:"batch_id"`
	CodeHash  string  `json:"-"`
	PlanCode  string  `json:"plan_code"`
	Kind      string  `json:"kind"`
	Amount    int64   `json:"amount"`
	Days      int     `json:"days"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	Status    string  `json:"status"`
	UsedBy    *int64  `json:"used_by,omitempty"`
	UsedAt    *string `json:"used_at,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// RedeemResult is the outcome of a successful redemption.
type RedeemResult struct {
	User    User   `json:"user"`
	BatchID int64  `json:"batch_id"`
	Kind    string `json:"kind"`
	Amount  int64  `json:"amount"`
	Days    int    `json:"days"`
}

const redemptionAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewRedemptionCode returns a grouped 16 character code (80 bits of entropy).
func NewRedemptionCode() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(19)
	for i, v := range buf {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(redemptionAlphabet[int(v)%len(redemptionAlphabet)])
	}
	return b.String(), nil
}

// NormalizeRedemptionCode removes separators, spaces and lowercases input.
func NormalizeRedemptionCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		switch r {
		case '-', ' ', '\t', '\n', '\r':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// HashRedemptionCode returns the stored SHA-256 digest.
func HashRedemptionCode(code string) string {
	sum := sha256.Sum256([]byte(NormalizeRedemptionCode(code)))
	return hex.EncodeToString(sum[:])
}

func newBatchNo() string {
	buf := make([]byte, 3)
	_, _ = rand.Read(buf)
	return "RD" + time.Now().Format("20060102150405") + "-" +
		strings.ToUpper(hex.EncodeToString(buf))
}

func normalizeExpiresAt(raw string) (*string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			if layout == "2006-01-02" {
				t = t.Add(24*time.Hour - time.Second)
			}
			v := t.Format("2006-01-02 15:04:05")
			return &v, nil
		}
	}
	return nil, errors.New("截止时间格式无效")
}

// CreateRedemptionBatch validates the request, generates the codes and stores
// them. The plaintext codes are returned once and never persisted.
func (s *Store) CreateRedemptionBatch(kind, planCode string, amount int64, days, quantity int, expiresAt, note string) (RedemptionBatch, []string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	planCode = strings.TrimSpace(planCode)
	if kind != string(plan.KindCount) && kind != string(plan.KindDuration) {
		return RedemptionBatch{}, nil, ErrCodeInvalidBatch
	}
	if quantity < 1 || quantity > 10000 {
		return RedemptionBatch{}, nil, errors.New("生成数量需在 1 到 10000 之间")
	}
	if len([]rune(note)) > 255 {
		return RedemptionBatch{}, nil, errors.New("备注过长")
	}
	if planCode == "" {
		if kind == string(plan.KindCount) {
			planCode = "count"
		} else {
			planCode = "monthly"
		}
	}
	p, ok := plan.Lookup(planCode)
	if !ok || string(p.Kind) != kind || p.Unlimited {
		return RedemptionBatch{}, nil, ErrCodeInvalidBatch
	}
	if kind == string(plan.KindCount) {
		if amount <= 0 {
			return RedemptionBatch{}, nil, errors.New("次数卡密需要大于 0 的额度")
		}
		days = 0
	} else {
		if p.Custom {
			if days <= 0 {
				return RedemptionBatch{}, nil, errors.New("时长卡密需要大于 0 的天数")
			}
		} else {
			// Fixed plans carry their own duration; ignore a stale form value.
			days = p.Days
		}
		amount = 0
	}
	exp, err := normalizeExpiresAt(expiresAt)
	if err != nil {
		return RedemptionBatch{}, nil, err
	}

	batch := RedemptionBatch{
		BatchNo:  newBatchNo(),
		Kind:     kind,
		PlanCode: planCode,
		Amount:   amount,
		Days:     days,
		Quantity: quantity,
		Note:     strings.TrimSpace(note),
	}
	plain := make([]string, 0, quantity)
	hashes := make([]string, 0, quantity)
	seen := make(map[string]bool, quantity)
	for len(plain) < quantity {
		code, err := NewRedemptionCode()
		if err != nil {
			return RedemptionBatch{}, nil, err
		}
		norm := NormalizeRedemptionCode(code)
		if seen[norm] {
			continue
		}
		seen[norm] = true
		plain = append(plain, code)
		hashes = append(hashes, HashRedemptionCode(code))
	}

	tx, err := s.db.Begin()
	if err != nil {
		return RedemptionBatch{}, nil, err
	}
	defer tx.Rollback()

	batchID, err := s.insertReturningIDTx(tx, `INSERT INTO RedemptionBatches
		(BatchNo, Kind, PlanCode, Amount, Days, Quantity, Note, CreatedAt)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())`,
		batch.BatchNo, batch.Kind, batch.PlanCode, batch.Amount, batch.Days, batch.Quantity, batch.Note)
	if err != nil {
		return RedemptionBatch{}, nil, err
	}
	for _, hash := range hashes {
		if _, err := execOn(s.dialect, tx, `INSERT INTO RedemptionCodes
			(BatchId, CodeHash, Kind, Amount, Days, ExpiresAt, Status, CreatedAt)
			VALUES ($1,$2,$3,$4,$5,$6,'unused',NOW())`,
			batchID, hash, batch.Kind, batch.Amount, batch.Days, exp); err != nil {
			if isUniqueViolation(err) {
				return RedemptionBatch{}, nil, errors.New("生成卡密冲突，请重试")
			}
			return RedemptionBatch{}, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RedemptionBatch{}, nil, err
	}
	batch.ID = batchID
	batch.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	return batch, plain, nil
}

// ListRedemptionBatches returns batches newest first with their used counts.
func (s *Store) ListRedemptionBatches(page, pageSize int) ([]RedemptionBatch, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	var total int64
	if err := s.queryRow(`SELECT COUNT(*) FROM RedemptionBatches`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.query(`SELECT b.Id, b.BatchNo, b.Kind, b.PlanCode, b.Amount, b.Days, b.Quantity, b.Note,
		`+s.dialect.formatTimestamp("b.CreatedAt")+`,
		(SELECT COUNT(*) FROM RedemptionCodes c WHERE c.BatchId = b.Id AND c.Status = 'used')
		FROM RedemptionBatches b ORDER BY b.Id DESC LIMIT $1 OFFSET $2`,
		pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []RedemptionBatch{}
	for rows.Next() {
		var b RedemptionBatch
		if err := rows.Scan(&b.ID, &b.BatchNo, &b.Kind, &b.PlanCode, &b.Amount, &b.Days,
			&b.Quantity, &b.Note, &b.CreatedAt, &b.UsedCount); err != nil {
			return nil, 0, err
		}
		out = append(out, b)
	}
	return out, total, rows.Err()
}

// RevokeRedemptionBatch invalidates every unused code in a batch.
func (s *Store) RevokeRedemptionBatch(batchID int64) (int64, error) {
	res, err := s.exec(`UPDATE RedemptionCodes SET Status = 'revoked'
		WHERE BatchId = $1 AND Status = 'unused'`, batchID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// RedeemCode applies one code to a user inside a single transaction.
func (s *Store) RedeemCode(userID int64, codeHash string) (RedeemResult, error) {
	codeHash = strings.TrimSpace(codeHash)
	if codeHash == "" {
		return RedeemResult{}, ErrCodeInvalidCodeInput
	}
	tx, err := s.db.Begin()
	if err != nil {
		return RedeemResult{}, err
	}
	defer tx.Rollback()

	var (
		code      RedemptionCode
		expiresAt sql.NullString
	)
	err = queryRowOn(s.dialect, tx, `SELECT Id, BatchId, Kind, Amount, Days, ExpiresAt, Status
		FROM RedemptionCodes WHERE CodeHash = $1 FOR UPDATE`, codeHash).
		Scan(&code.ID, &code.BatchID, &code.Kind, &code.Amount, &code.Days, &expiresAt, &code.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return RedeemResult{}, ErrCodeInvalid
	}
	if err != nil {
		return RedeemResult{}, err
	}
	if code.Status != "unused" {
		return RedeemResult{}, ErrCodeInvalid
	}
	if err := queryRowOn(s.dialect, tx, `SELECT PlanCode FROM RedemptionBatches WHERE Id = $1`, code.BatchID).
		Scan(&code.PlanCode); err != nil {
		return RedeemResult{}, err
	}
	now := time.Now()
	if expiresAt.Valid && expiresAt.String != "" {
		if t, err := parseTime(expiresAt.String); err == nil && now.After(t) {
			return RedeemResult{}, ErrCodeExpired
		}
	}

	u, err := scanUser(queryRowOn(s.dialect, tx, `SELECT `+s.userColumns()+` FROM Users WHERE Id = $1 FOR UPDATE`, userID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return RedeemResult{}, ErrUserNotFound
	}
	if err != nil {
		return RedeemResult{}, err
	}
	if !u.Enabled {
		return RedeemResult{}, ErrRedeemUserDisabled
	}

	next, err := applyRedemption(u, code, now)
	if err != nil {
		return RedeemResult{}, err
	}
	if _, err := execOn(s.dialect, tx, `UPDATE Users
		SET PlanType=$1, PlanCode=$2, PlanLabel=$3, StartAt=$4, ExpireAt=$5,
		    TotalCount=$6, RemainCount=$7, UsedCount=$8, UpdatedAt=NOW()
		WHERE Id=$9`,
		next.PlanType, next.PlanCode, next.PlanLabel, next.StartAt, next.ExpireAt,
		next.TotalCount, next.RemainCount, next.UsedCount, u.ID); err != nil {
		return RedeemResult{}, err
	}
	res, err := execOn(s.dialect, tx, `UPDATE RedemptionCodes SET Status='used', UsedBy=$1, UsedAt=$2
		WHERE Id=$3 AND Status='unused'`, u.ID, now.Format("2006-01-02 15:04:05"), code.ID)
	if err != nil {
		return RedeemResult{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return RedeemResult{}, ErrCodeInvalid
	}
	detail, _ := json.Marshal(map[string]any{
		"batch_id": code.BatchID,
		"kind":     code.Kind,
		"amount":   code.Amount,
		"days":     code.Days,
	})
	if _, err := execOn(s.dialect, tx, `INSERT INTO UserAuditLogs
		(Action, UserId, UserToken, Detail, Actor, CreatedAt)
		VALUES ('redeem', $1, $2, $3, 'user', NOW())`,
		u.ID, u.Token, string(detail)); err != nil {
		return RedeemResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RedeemResult{}, err
	}
	updated, err := s.GetUserByID(u.ID)
	if err != nil {
		return RedeemResult{}, err
	}
	return RedeemResult{User: updated, BatchID: code.BatchID, Kind: code.Kind, Amount: code.Amount, Days: code.Days}, nil
}

// applyRedemption computes the user's next plan without touching the database.
func applyRedemption(u User, code RedemptionCode, now time.Time) (User, error) {
	next := u
	zeroQuota := u.PlanType == string(plan.KindCount) &&
		u.TotalCount != nil && *u.TotalCount == 0 &&
		u.RemainCount != nil && *u.RemainCount == 0 &&
		u.UsedCount == 0

	var currentExpiry time.Time
	hasExpiry := false
	if u.ExpireAt != nil {
		if t, err := parseTime(*u.ExpireAt); err == nil {
			currentExpiry = t
			hasExpiry = true
		}
	}

	switch code.Kind {
	case string(plan.KindCount):
		if !zeroQuota && u.PlanType != string(plan.KindCount) {
			if !hasExpiry {
				return next, ErrCodeNoNeed
			}
			if currentExpiry.After(now) {
				return next, ErrCodeNeedsDuration
			}
		}
		next.PlanType = string(plan.KindCount)
		next.PlanCode = code.PlanCode
		next.PlanLabel = planLabel(code.PlanCode, "按次")
		next.ExpireAt = nil
		if u.PlanType == string(plan.KindCount) {
			total := int64(0)
			if u.TotalCount != nil {
				total = *u.TotalCount
			}
			remain := int64(0)
			if u.RemainCount != nil {
				remain = *u.RemainCount
			}
			total += code.Amount
			remain += code.Amount
			next.TotalCount = int64ptr(total)
			next.RemainCount = int64ptr(remain)
			if hasExpiry && currentExpiry.After(now) {
				exp := currentExpiry.Format("2006-01-02 15:04:05")
				next.ExpireAt = &exp
			}
		} else {
			next.StartAt = now.Format("2006-01-02 15:04:05")
			next.TotalCount = int64ptr(code.Amount)
			next.RemainCount = int64ptr(code.Amount)
			next.UsedCount = 0
		}
	case string(plan.KindDuration):
		if !zeroQuota && u.PlanType == string(plan.KindCount) {
			if u.RemainCount != nil && *u.RemainCount > 0 {
				return next, ErrCodeNeedsCount
			}
		}
		if !zeroQuota && u.PlanType == string(plan.KindDuration) && !hasExpiry {
			return next, ErrCodeNoNeed
		}
		base := now
		if hasExpiry && currentExpiry.After(now) {
			base = currentExpiry
		}
		exp := base.AddDate(0, 0, code.Days).Format("2006-01-02 15:04:05")
		if u.PlanType != string(plan.KindDuration) {
			next.StartAt = now.Format("2006-01-02 15:04:05")
			next.UsedCount = 0
		}
		next.PlanType = string(plan.KindDuration)
		next.PlanCode = code.PlanCode
		next.PlanLabel = planLabel(code.PlanCode, "时长套餐")
		next.TotalCount = nil
		next.RemainCount = nil
		next.ExpireAt = &exp
	default:
		return next, fmt.Errorf("%w: %s", ErrCodeInvalid, code.Kind)
	}
	return next, nil
}

func planLabel(code, fallback string) string {
	if p, ok := plan.Lookup(code); ok && p.Label != "" {
		return p.Label
	}
	return fallback
}
