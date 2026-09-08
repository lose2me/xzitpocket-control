package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Store) GetIdentityByHash(ctx context.Context, provider, hash string) (Identity, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, user_id, provider, student_id_hash, student_alias, student_id_ciphertext, created_at, updated_at FROM identities WHERE provider = ? AND student_id_hash = ?",
		provider, hash)
	return scanIdentity(row)
}

func (s *Store) GetIdentityByUser(ctx context.Context, userID, provider string) (Identity, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, user_id, provider, student_id_hash, student_alias, student_id_ciphertext, created_at, updated_at FROM identities WHERE user_id = ? AND provider = ? LIMIT 1",
		userID, provider)
	return scanIdentity(row)
}

func (s *Store) CreateUser(ctx context.Context, user User) error {
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO users(id, status, display_name, created_at, last_login_at) VALUES (?, ?, ?, ?, ?)",
		user.ID, user.Status, user.DisplayName, millis(user.CreatedAt), millis(user.LastLoginAt))
	return err
}

// FindOrCreateIdentity serializes the first-login race for a student identity.
// The returned identity is always the canonical row, whether it was created by
// this call or by a concurrent login.
func (s *Store) FindOrCreateIdentity(ctx context.Context, user User, identity Identity) (Identity, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Identity{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var existing Identity
	var created, updated int64
	row := tx.QueryRowContext(ctx, "SELECT id, user_id, provider, student_id_hash, student_alias, student_id_ciphertext, created_at, updated_at FROM identities WHERE provider = ? AND student_id_hash = ?", identity.Provider, identity.StudentIDHash)
	if scanErr := row.Scan(&existing.ID, &existing.UserID, &existing.Provider, &existing.StudentIDHash, &existing.StudentAlias, &existing.StudentIDCiphertext, &created, &updated); scanErr == nil {
		existing.CreatedAt, existing.UpdatedAt = fromMillis(created), fromMillis(updated)
		if err := tx.Commit(); err != nil {
			return Identity{}, err
		}
		return existing, nil
	} else if scanErr != sql.ErrNoRows {
		return Identity{}, scanErr
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO users(id, status, display_name, created_at, last_login_at) VALUES (?, ?, ?, ?, ?)", user.ID, user.Status, user.DisplayName, millis(user.CreatedAt), millis(user.LastLoginAt)); err != nil {
		return Identity{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO identities(id, user_id, provider, student_id_hash, student_alias, student_id_ciphertext, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", identity.ID, identity.UserID, identity.Provider, identity.StudentIDHash, identity.StudentAlias, identity.StudentIDCiphertext, millis(identity.CreatedAt), millis(identity.UpdatedAt)); err != nil {
		return Identity{}, err
	}
	if err := tx.Commit(); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func (s *Store) UpdateIdentity(ctx context.Context, identity Identity) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE identities SET student_alias = ?, student_id_ciphertext = ?, updated_at = ? WHERE id = ?",
		identity.StudentAlias, identity.StudentIDCiphertext, millis(identity.UpdatedAt), identity.ID)
	return err
}

func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, status, display_name, created_at, last_login_at FROM users WHERE id = ?", id)
	return scanUser(row)
}

func (s *Store) UpdateUserLogin(ctx context.Context, id, displayName string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE users SET display_name = CASE WHEN ? <> '' THEN ? ELSE display_name END, last_login_at = ? WHERE id = ?",
		displayName, displayName, millis(now), id)
	return err
}

func (s *Store) TouchUserConnection(ctx context.Context, id string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE users SET last_login_at = ? WHERE id = ?", millis(now), id)
	return err
}

type UserListItem struct {
	User
	DeviceCount int `json:"device_count"`
}

func (s *Store) ListUsers(ctx context.Context, limit, offset int, status string) ([]UserListItem, error) {
	args := []any{limit, offset}
	query := "SELECT u.id, u.status, u.display_name, u.created_at, u.last_login_at, COUNT(DISTINCT CASE WHEN ud.unbound_at IS NULL THEN ud.device_id END) FROM users u LEFT JOIN user_devices ud ON ud.user_id = u.id"
	if strings.TrimSpace(status) != "" {
		query += " WHERE u.status = ?"
		args = []any{status, limit, offset}
	}
	query += " GROUP BY u.id ORDER BY u.last_login_at DESC LIMIT ? OFFSET ?"
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []UserListItem
	for rows.Next() {
		var u UserListItem
		var created, lastLogin int64
		if err := rows.Scan(&u.ID, &u.Status, &u.DisplayName, &created, &lastLogin, &u.DeviceCount); err != nil {
			return nil, err
		}
		u.CreatedAt = fromMillis(created)
		u.LastLoginAt = fromMillis(lastLogin)
		result = append(result, u)
	}
	return result, rows.Err()
}

func (s *Store) CountUsers(ctx context.Context, status string) (int, error) {
	var count int
	var err error
	if strings.TrimSpace(status) == "" {
		err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
	} else {
		err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE status = ?", status).Scan(&count)
	}
	return count, err
}

func (s *Store) SetUserStatus(ctx context.Context, id, status string) error {
	if status != "active" && status != "disabled" {
		return fmt.Errorf("invalid user status")
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE users SET status = ? WHERE id = ?", status, id)
	return err
}

type rowScannerUser interface {
	Scan(dest ...any) error
}

func scanUser(row rowScannerUser) (User, error) {
	var user User
	var created, lastLogin int64
	if err := row.Scan(&user.ID, &user.Status, &user.DisplayName, &created, &lastLogin); err != nil {
		return User{}, err
	}
	user.CreatedAt = fromMillis(created)
	user.LastLoginAt = fromMillis(lastLogin)
	return user, nil
}

func scanIdentity(row rowScannerUser) (Identity, error) {
	var identity Identity
	var created, updated int64
	if err := row.Scan(&identity.ID, &identity.UserID, &identity.Provider,
		&identity.StudentIDHash, &identity.StudentAlias, &identity.StudentIDCiphertext,
		&created, &updated); err != nil {
		return Identity{}, err
	}
	identity.CreatedAt = fromMillis(created)
	identity.UpdatedAt = fromMillis(updated)
	return identity, nil
}
