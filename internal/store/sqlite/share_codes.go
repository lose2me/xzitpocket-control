package sqlite

import (
	"context"
	"time"
)

type ShareCode struct {
	ID                string
	CodeHash          string
	CodeCiphertext    string
	PayloadCiphertext string
	CreatedByUserID   string
	CreatedAt         time.Time
	ExpiresAt         time.Time
}

func (s *Store) CreateShareCode(ctx context.Context, code ShareCode) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO share_codes(id, code_hash, code_ciphertext, payload_ciphertext, created_by_user_id, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		code.ID, code.CodeHash, code.CodeCiphertext, code.PayloadCiphertext, nullableString(code.CreatedByUserID), millis(code.CreatedAt), millis(code.ExpiresAt))
	return err
}

func (s *Store) GetShareCodeByHash(ctx context.Context, codeHash string) (ShareCode, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, code_hash, code_ciphertext, payload_ciphertext, COALESCE(created_by_user_id, ''), created_at, expires_at
		FROM share_codes WHERE code_hash = ?`, codeHash)
	return scanShareCode(row)
}

func (s *Store) ListShareCodes(ctx context.Context, limit, offset int) ([]ShareCode, int, error) {
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM share_codes").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, code_hash, code_ciphertext, payload_ciphertext, COALESCE(created_by_user_id, ''), created_at, expires_at
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
		SELECT id, code_hash, code_ciphertext, payload_ciphertext, COALESCE(created_by_user_id, ''), created_at, expires_at
		FROM share_codes WHERE id = ?`, id)
	return scanShareCode(row)
}

func (s *Store) DeleteShareCode(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM share_codes WHERE id = ?", id)
	return err
}

type shareCodeScanner interface {
	Scan(dest ...any) error
}

func scanShareCode(row shareCodeScanner) (ShareCode, error) {
	var code ShareCode
	var created, expires int64
	if err := row.Scan(&code.ID, &code.CodeHash, &code.CodeCiphertext, &code.PayloadCiphertext, &code.CreatedByUserID, &created, &expires); err != nil {
		return ShareCode{}, err
	}
	code.CreatedAt = fromMillis(created)
	code.ExpiresAt = fromMillis(expires)
	return code, nil
}
