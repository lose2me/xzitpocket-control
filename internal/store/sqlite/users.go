package sqlite

import (
	"context"
	"database/sql"
	"errors"
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
		"INSERT INTO users(id, status, display_name, college_name, class_name, created_at, last_login_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		user.ID, user.Status, user.DisplayName, user.CollegeName, user.ClassName, millis(user.CreatedAt), millis(user.LastLoginAt))
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
	} else if !errors.Is(scanErr, sql.ErrNoRows) {
		return Identity{}, scanErr
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO users(id, status, display_name, college_name, class_name, created_at, last_login_at) VALUES (?, ?, ?, ?, ?, ?, ?)", user.ID, user.Status, user.DisplayName, user.CollegeName, user.ClassName, millis(user.CreatedAt), millis(user.LastLoginAt)); err != nil {
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
		"SELECT id, status, display_name, college_name, class_name, created_at, last_login_at FROM users WHERE id = ?", id)
	return scanUser(row)
}

func (s *Store) UpdateUserLogin(ctx context.Context, id, displayName, collegeName, className string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET
			display_name = CASE WHEN ? <> '' THEN ? ELSE display_name END,
			college_name = CASE WHEN ? <> '' THEN ? ELSE college_name END,
			class_name = CASE WHEN ? <> '' THEN ? ELSE class_name END,
			last_login_at = ? WHERE id = ?`,
		displayName, displayName, collegeName, collegeName, className, className, millis(now), id)
	return err
}

func (s *Store) TouchUserConnection(ctx context.Context, id string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE users SET last_login_at = ? WHERE id = ?", millis(now), id)
	return err
}

type UserListItem struct {
	User
	Seq         int    `json:"seq"`
	DeviceCount int    `json:"device_count"`
	AppVersion  string `json:"app_version"`
}

const userListQuery = `SELECT u.id, u.status, u.display_name, u.college_name, u.class_name, u.created_at, u.last_login_at,
	r.seq AS seq,
	COUNT(DISTINCT CASE WHEN ud.unbound_at IS NULL THEN ud.device_id END) AS device_count,
	COALESCE((SELECT d.app_version FROM user_devices ud2 JOIN devices d ON d.id = ud2.device_id
		WHERE ud2.user_id = u.id AND ud2.unbound_at IS NULL AND d.revoked_at IS NULL
		ORDER BY d.last_seen_at DESC LIMIT 1), '') AS app_version
	FROM users u
	LEFT JOIN (SELECT id, ROW_NUMBER() OVER (ORDER BY created_at, id) AS seq FROM users) r ON r.id = u.id
	LEFT JOIN user_devices ud ON ud.user_id = u.id`

func (s *Store) ListUsers(ctx context.Context, limit, offset int, status, search, sort, order string) ([]UserListItem, error) {
	where, filterArgs := userListWhere(status, search)
	args := append(append([]any{}, filterArgs...), limit, offset)
	rows, err := s.DB.QueryContext(ctx, userListQuery+where+" GROUP BY u.id ORDER BY "+userListOrderBy(sort, order)+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []UserListItem
	for rows.Next() {
		var u UserListItem
		var created, lastLogin int64
		if err := rows.Scan(&u.ID, &u.Status, &u.DisplayName, &u.CollegeName, &u.ClassName, &created, &lastLogin, &u.Seq, &u.DeviceCount, &u.AppVersion); err != nil {
			return nil, err
		}
		u.CreatedAt = fromMillis(created)
		u.LastLoginAt = fromMillis(lastLogin)
		result = append(result, u)
	}
	return result, rows.Err()
}

// userListSorts whitelists the ORDER BY targets, so only these fixed strings
// ever reach the SQL text. Plain user columns are qualified because
// user_devices also exposes last_login_at; aggregates use their output alias.
var userListSorts = map[string]string{
	"seq":           "r.seq",
	"created_at":    "u.created_at",
	"last_login_at": "u.last_login_at",
	"display_name":  "u.display_name",
	"college_name":  "u.college_name",
	"class_name":    "u.class_name",
	"status":        "u.status",
	"device_count":  "device_count",
	"app_version":   "app_version",
}

// ValidUserListSort reports whether the store accepts the given sort key.
func ValidUserListSort(sort string) bool {
	_, ok := userListSorts[strings.TrimSpace(sort)]
	return ok
}

// userListDefaultOrderBy is used whenever the requested sort is missing or not
// whitelisted, so the fallback is independent of the supplied direction.
const userListDefaultOrderBy = "u.last_login_at DESC, u.id"

func userListOrderBy(sort, order string) string {
	column, ok := userListSorts[strings.TrimSpace(sort)]
	if !ok {
		return userListDefaultOrderBy
	}
	direction := "DESC"
	if strings.EqualFold(strings.TrimSpace(order), "asc") {
		direction = "ASC"
	}
	return column + " " + direction + ", u.id"
}

// userListWhere filters the admin user list by account status and by a free-text
// query matched against college, class and display name.
func userListWhere(status, search string) (string, []any) {
	conditions := make([]string, 0, 2)
	args := []any{}
	if status = strings.TrimSpace(status); status != "" {
		conditions = append(conditions, "u.status = ?")
		args = append(args, status)
	}
	if search = strings.TrimSpace(search); search != "" {
		like := "%" + escapeLike(search) + "%"
		conditions = append(conditions, "(u.display_name LIKE ? ESCAPE '\\' OR u.college_name LIKE ? ESCAPE '\\' OR u.class_name LIKE ? ESCAPE '\\')")
		args = append(args, like, like, like)
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func (s *Store) CountUsers(ctx context.Context, status, search string) (int, error) {
	where, args := userListWhere(status, search)
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users u"+where, args...).Scan(&count)
	return count, err
}

func (s *Store) SetUserStatus(ctx context.Context, id, status string) error {
	if status != "active" && status != "disabled" {
		return fmt.Errorf("invalid user status")
	}
	result, err := s.DB.ExecContext(ctx, "UPDATE users SET status = ? WHERE id = ?", status, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

type rowScannerUser interface {
	Scan(dest ...any) error
}

func scanUser(row rowScannerUser) (User, error) {
	var user User
	var created, lastLogin int64
	if err := row.Scan(&user.ID, &user.Status, &user.DisplayName, &user.CollegeName, &user.ClassName, &created, &lastLogin); err != nil {
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
