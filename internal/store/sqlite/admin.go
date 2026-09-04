package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"time"
)

type Admin struct {
	ID           string
	PasswordHash string
	CreatedAt    time.Time
	LastLoginAt  time.Time
	DisabledAt   *time.Time
}

// GetAdmin returns the single local administrator.
func (s *Store) GetAdmin(ctx context.Context) (Admin, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, password_hash, created_at, last_login_at, disabled_at FROM admin_users ORDER BY created_at, id LIMIT 1")
	return scanAdmin(row)
}

func (s *Store) GetAdminByID(ctx context.Context, id string) (Admin, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, password_hash, created_at, last_login_at, disabled_at FROM admin_users WHERE id = ?",
		id)
	return scanAdmin(row)
}

func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_users").Scan(&count)
	return count, err
}

func (s *Store) CreateAdmin(ctx context.Context, admin Admin) error {
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO admin_users(id, password_hash, created_at, last_login_at) VALUES (?, ?, ?, ?)",
		admin.ID, admin.PasswordHash, millis(admin.CreatedAt), millis(admin.LastLoginAt))
	return err
}

func (s *Store) TouchAdmin(ctx context.Context, id string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE admin_users SET last_login_at = ? WHERE id = ? AND disabled_at IS NULL",
		millis(now), id)
	return err
}

func (s *Store) CreateAdminSession(ctx context.Context, id, tokenHash string, expires, now time.Time) error {
	sessionID, err := newStoreID("as")
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		"INSERT INTO admin_sessions(id, admin_id, token_hash, expires_at, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?)",
		sessionID, id, tokenHash, millis(expires), millis(now), millis(now))
	return err
}

type AdminSession struct {
	ID         string
	AdminID    string
	TokenHash  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastUsedAt time.Time
	RevokedAt  *time.Time
}

func (s *Store) GetAdminSession(ctx context.Context, tokenHash string) (AdminSession, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, admin_id, token_hash, expires_at, created_at, last_used_at, revoked_at FROM admin_sessions WHERE token_hash = ?",
		tokenHash)
	var session AdminSession
	var expires, created, lastUsed int64
	var revoked sql.NullInt64
	if err := row.Scan(&session.ID, &session.AdminID, &session.TokenHash, &expires, &created, &lastUsed, &revoked); err != nil {
		return AdminSession{}, err
	}
	session.ExpiresAt = fromMillis(expires)
	session.CreatedAt = fromMillis(created)
	session.LastUsedAt = fromMillis(lastUsed)
	session.RevokedAt = nullableTime(revoked)
	return session, nil
}

func (s *Store) TouchAdminSession(ctx context.Context, id string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE admin_sessions SET last_used_at = ? WHERE id = ? AND revoked_at IS NULL",
		millis(now), id)
	return err
}

func (s *Store) RevokeAdminSession(ctx context.Context, id string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE admin_sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL",
		millis(now), id)
	return err
}

type AuditLog struct {
	ID         int64     `json:"id"`
	ActorID    string    `json:"actor_id,omitempty"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type,omitempty"`
	TargetID   string    `json:"target_id,omitempty"`
	Detail     string    `json:"detail"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Store) AddAudit(ctx context.Context, actorID, action, targetType, targetID, detail string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO audit_logs(actor_id, action, target_type, target_id, detail_json, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		nullableString(actorID), action, nullableString(targetType), nullableString(targetID), detail, millis(now))
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit, offset int) ([]AuditLog, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT id, actor_id, action, target_type, target_id, detail_json, created_at FROM audit_logs ORDER BY created_at DESC LIMIT ? OFFSET ?",
		limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []AuditLog
	for rows.Next() {
		var log AuditLog
		var actor, targetType, targetID sql.NullString
		var created int64
		if err := rows.Scan(&log.ID, &actor, &log.Action, &targetType, &targetID, &log.Detail, &created); err != nil {
			return nil, err
		}
		log.ActorID = actor.String
		log.TargetType = targetType.String
		log.TargetID = targetID.String
		log.CreatedAt = fromMillis(created)
		result = append(result, log)
	}
	return result, rows.Err()
}

func scanAdmin(row rowScannerUser) (Admin, error) {
	var admin Admin
	var created, lastLogin int64
	var disabled sql.NullInt64
	if err := row.Scan(&admin.ID, &admin.PasswordHash, &created, &lastLogin, &disabled); err != nil {
		return Admin{}, err
	}
	admin.CreatedAt = fromMillis(created)
	admin.LastLoginAt = fromMillis(lastLogin)
	admin.DisabledAt = nullableTime(disabled)
	return admin, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func newStoreID(prefix string) (string, error) {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return "", err
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(token), nil
}
