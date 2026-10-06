package sqlite

import (
	"context"
	"database/sql"
	"time"
)

type ShareCode struct {
	ID                string
	CodeHash          string
	CodeCiphertext    string
	PayloadCiphertext string
	PayloadHash       string
	CreatedByUserID   string
	CreatedAt         time.Time
	ExpiresAt         time.Time
}

func (s *Store) CreateShareCode(ctx context.Context, code ShareCode) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO share_codes(id, code_hash, code_ciphertext, payload_ciphertext, payload_hash, created_by_user_id, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		code.ID, code.CodeHash, code.CodeCiphertext, code.PayloadCiphertext, code.PayloadHash, nullableString(code.CreatedByUserID), millis(code.CreatedAt), millis(code.ExpiresAt))
	return err
}

func (s *Store) GetShareCodeByHash(ctx context.Context, codeHash string) (ShareCode, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, code_hash, code_ciphertext, payload_ciphertext, payload_hash, COALESCE(created_by_user_id, ''), created_at, expires_at
		FROM share_codes WHERE code_hash = ?`, codeHash)
	return scanShareCode(row)
}

func (s *Store) ListShareCodes(ctx context.Context, limit, offset int) ([]ShareCode, int, error) {
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM share_codes").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, code_hash, code_ciphertext, payload_ciphertext, payload_hash, COALESCE(created_by_user_id, ''), created_at, expires_at
		FROM share_codes ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]ShareCode, 0)
	for rows.Next() {
		item, err := scanShareCode(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Store) GetShareCodeByID(ctx context.Context, id string) (ShareCode, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, code_hash, code_ciphertext, payload_ciphertext, payload_hash, COALESCE(created_by_user_id, ''), created_at, expires_at
		FROM share_codes WHERE id = ?`, id)
	return scanShareCode(row)
}

func (s *Store) DeleteShareCode(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM share_codes WHERE id = ?", id)
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

type shareCodeScanner interface {
	Scan(dest ...any) error
}

func scanShareCode(row shareCodeScanner) (ShareCode, error) {
	var code ShareCode
	var created, expires int64
	if err := row.Scan(&code.ID, &code.CodeHash, &code.CodeCiphertext, &code.PayloadCiphertext, &code.PayloadHash, &code.CreatedByUserID, &created, &expires); err != nil {
		return ShareCode{}, err
	}
	code.CreatedAt = fromMillis(created)
	code.ExpiresAt = fromMillis(expires)
	return code, nil
}

func (s *Store) FindActiveShareCodesByUserPayload(ctx context.Context, userID, payloadHash string, now time.Time) ([]ShareCode, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, code_hash, code_ciphertext, payload_ciphertext, payload_hash, COALESCE(created_by_user_id, ''), created_at, expires_at
		FROM share_codes
		WHERE created_by_user_id = ? AND payload_hash = ? AND expires_at > ?
		ORDER BY created_at DESC`, userID, payloadHash, millis(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ShareCode, 0)
	for rows.Next() {
		item, err := scanShareCode(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) RenewShareCode(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE share_codes SET expires_at = ? WHERE id = ?", millis(expiresAt), id)
	return err
}
