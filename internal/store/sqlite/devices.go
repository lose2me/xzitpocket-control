package sqlite

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) CreateDevice(ctx context.Context, d Device) error {
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO devices (id, device_serial, installation_id, device_token_hash, public_key, platform, app_version, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		d.ID, d.DeviceSerial, d.Installation, d.TokenHash, d.PublicKey, d.Platform, d.AppVersion,
		millis(d.CreatedAt), millis(d.LastSeenAt))
	return err
}

func (s *Store) GetDeviceByTokenHash(ctx context.Context, tokenHash string) (Device, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, device_serial, installation_id, device_token_hash, public_key, platform, app_version, created_at, last_seen_at, revoked_at FROM devices WHERE device_token_hash = ?", tokenHash)
	return scanDevice(row)
}

func (s *Store) GetDeviceByID(ctx context.Context, id string) (Device, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, device_serial, installation_id, device_token_hash, public_key, platform, app_version, created_at, last_seen_at, revoked_at FROM devices WHERE id = ?", id)
	return scanDevice(row)
}

func (s *Store) GetDeviceByInstallation(ctx context.Context, installationID string) (Device, error) {
	row := s.DB.QueryRowContext(ctx,
		"SELECT id, device_serial, installation_id, device_token_hash, public_key, platform, app_version, created_at, last_seen_at, revoked_at FROM devices WHERE installation_id = ?", installationID)
	return scanDevice(row)
}

func (s *Store) TouchDevice(ctx context.Context, id string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"UPDATE devices SET last_seen_at = ? WHERE id = ? AND revoked_at IS NULL",
		millis(now), id)
	return err
}

func (s *Store) ListDevices(ctx context.Context, limit, offset int) ([]Device, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT id, device_serial, installation_id, device_token_hash, public_key, platform, app_version, created_at, last_seen_at, revoked_at FROM devices ORDER BY last_seen_at DESC LIMIT ? OFFSET ?",
		limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func (s *Store) CountDevices(ctx context.Context) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM devices").Scan(&count)
	return count, err
}

func (s *Store) BindUserDevice(ctx context.Context, userID, deviceID string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO user_devices(user_id, device_id, first_bound_at, last_login_at) VALUES (?, ?, ?, ?) ON CONFLICT(user_id, device_id) DO UPDATE SET last_login_at = excluded.last_login_at, unbound_at = NULL",
		userID, deviceID, millis(now), millis(now))
	return err
}

func (s *Store) CountUserDevices(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM user_devices WHERE user_id = ? AND unbound_at IS NULL", userID).Scan(&count)
	return count, err
}

type UserDevice struct {
	Device
	FirstBoundAt time.Time
	LastLoginAt  time.Time
	UnboundAt    *time.Time
}

func (s *Store) ListUserDevices(ctx context.Context, userID string) ([]UserDevice, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT d.id, d.device_serial, d.installation_id, d.device_token_hash, d.public_key, d.platform, d.app_version, d.created_at, d.last_seen_at, d.revoked_at, ud.first_bound_at, ud.last_login_at, ud.unbound_at FROM user_devices ud JOIN devices d ON d.id = ud.device_id WHERE ud.user_id = ? ORDER BY ud.last_login_at DESC",
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []UserDevice
	for rows.Next() {
		var d Device
		var created, lastSeen, firstBound, lastLogin int64
		var revoked, unbound sql.NullInt64
		if err := rows.Scan(&d.ID, &d.DeviceSerial, &d.Installation, &d.TokenHash, &d.PublicKey,
			&d.Platform, &d.AppVersion, &created, &lastSeen, &revoked, &firstBound, &lastLogin, &unbound); err != nil {
			return nil, err
		}
		d.CreatedAt = fromMillis(created)
		d.LastSeenAt = fromMillis(lastSeen)
		d.RevokedAt = nullableTime(revoked)
		result = append(result, UserDevice{Device: d, FirstBoundAt: fromMillis(firstBound), LastLoginAt: fromMillis(lastLogin), UnboundAt: nullableTime(unbound)})
	}
	return result, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDevice(row rowScanner) (Device, error) {
	var d Device
	var created, lastSeen int64
	var revoked sql.NullInt64
	if err := row.Scan(&d.ID, &d.DeviceSerial, &d.Installation, &d.TokenHash, &d.PublicKey,
		&d.Platform, &d.AppVersion, &created, &lastSeen, &revoked); err != nil {
		return Device{}, err
	}
	d.CreatedAt = fromMillis(created)
	d.LastSeenAt = fromMillis(lastSeen)
	d.RevokedAt = nullableTime(revoked)
	return d, nil
}
