package sqlite

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) CreateChallenge(ctx context.Context, challenge Challenge) error {
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO auth_challenges(id, device_id, challenge_hash, expires_at) VALUES (?, ?, ?, ?)",
		challenge.ID, challenge.DeviceID, challenge.ChallengeHash, millis(challenge.ExpiresAt))
	return err
}

func (s *Store) ConsumeChallenge(ctx context.Context, id, deviceID, challengeHash string, now time.Time) (bool, error) {
	result, err := s.DB.ExecContext(ctx,
		"UPDATE auth_challenges SET used_at = ? WHERE id = ? AND device_id = ? AND challenge_hash = ? AND used_at IS NULL AND expires_at > ?",
		millis(now), id, deviceID, challengeHash, millis(now))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) CreateSession(ctx context.Context, session Session) error {
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO sessions(id, user_id, device_id, access_hash, refresh_hash, expires_at, refresh_expires_at, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		session.ID, session.UserID, session.DeviceID, session.AccessHash, session.RefreshHash,
		millis(session.ExpiresAt), millis(session.RefreshExpires), millis(session.CreatedAt), millis(session.LastUsedAt))
	return err
}

func (s *Store) GetSessionByAccessHash(ctx context.Context, hash string) (Session, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, user_id, device_id, access_hash, refresh_hash, expires_at, refresh_expires_at, created_at, last_used_at, revoked_at FROM sessions WHERE access_hash = ?",
		hash)
	return scanSession(row)
}

func (s *Store) GetSessionByRefreshHash(ctx context.Context, hash string) (Session, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, user_id, device_id, access_hash, refresh_hash, expires_at, refresh_expires_at, created_at, last_used_at, revoked_at FROM sessions WHERE refresh_hash = ?",
		hash)
	return scanSession(row)
}

func (s *Store) TouchSession(ctx context.Context, id string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE sessions SET last_used_at = ? WHERE id = ? AND revoked_at IS NULL",
		millis(now), id)
	return err
}

func (s *Store) RotateSession(ctx context.Context, id, oldRefreshHash, newAccessHash, newRefreshHash string, accessExpiry, refreshExpiry, now time.Time) (Session, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	result, err := tx.ExecContext(ctx,
		"UPDATE sessions SET access_hash = ?, refresh_hash = ?, expires_at = ?, refresh_expires_at = ?, last_used_at = ? WHERE id = ? AND refresh_hash = ? AND revoked_at IS NULL AND refresh_expires_at > ?",
		newAccessHash, newRefreshHash, millis(accessExpiry), millis(refreshExpiry), millis(now),
		id, oldRefreshHash, millis(now))
	if err != nil {
		return Session{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Session{}, err
	}
	if affected != 1 {
		return Session{}, sql.ErrNoRows
	}
	row := tx.QueryRowContext(ctx,
		"SELECT id, user_id, device_id, access_hash, refresh_hash, expires_at, refresh_expires_at, created_at, last_used_at, revoked_at FROM sessions WHERE id = ?",
		id)
	session, err := scanSession(row)
	if err != nil {
		return Session{}, err
	}
	if err = tx.Commit(); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *Store) RevokeSession(ctx context.Context, id string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL",
		millis(now), id)
	return err
}

func (s *Store) RevokeUserDeviceSessions(ctx context.Context, userID, deviceID string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND device_id = ? AND revoked_at IS NULL",
		millis(now), userID, deviceID)
	return err
}

func (s *Store) RevokeDeviceSessionsExcept(ctx context.Context, deviceID, userID string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE sessions SET revoked_at = ? WHERE device_id = ? AND user_id <> ? AND revoked_at IS NULL",
		millis(now), deviceID, userID)
	return err
}

func (s *Store) CountActiveSessions(ctx context.Context, userID string, now time.Time) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sessions WHERE user_id = ? AND revoked_at IS NULL AND refresh_expires_at > ?",
		userID, millis(now)).Scan(&count)
	return count, err
}

func (s *Store) CleanupChallenges(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM auth_challenges WHERE expires_at < ? OR used_at IS NOT NULL AND used_at < ?", millis(before), millis(before))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func scanSession(row rowScannerUser) (Session, error) {
	var session Session
	var expires, refreshExpires, created, lastUsed int64
	var revoked sql.NullInt64
	if err := row.Scan(&session.ID, &session.UserID, &session.DeviceID,
		&session.AccessHash, &session.RefreshHash, &expires, &refreshExpires,
		&created, &lastUsed, &revoked); err != nil {
		return Session{}, err
	}
	session.ExpiresAt = fromMillis(expires)
	session.RefreshExpires = fromMillis(refreshExpires)
	session.CreatedAt = fromMillis(created)
	session.LastUsedAt = fromMillis(lastUsed)
	session.RevokedAt = nullableTime(revoked)
	return session, nil
}
