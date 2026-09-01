package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var embeddedSchema string

type Store struct {
	DB   *sql.DB
	Path string
}

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	return &Store{DB: db, Path: path}, nil
}

func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, embeddedSchema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (1, ?)", millis(time.Now().UTC())); err != nil {
		return fmt.Errorf("record schema migration: %w", err)
	}
	return nil
}

type Device struct {
	ID           string     `json:"id"`
	DeviceSerial string     `json:"device_serial"`
	Installation string     `json:"installation_id"`
	TokenHash    string     `json:"-"`
	PublicKey    string     `json:"-"`
	Platform     string     `json:"platform"`
	AppVersion   string     `json:"app_version"`
	CreatedAt    time.Time  `json:"created_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

type User struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	DisplayName string    `json:"display_name"`
	StudentID   string    `json:"-"`
	Alias       string    `json:"student_alias,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastLoginAt time.Time `json:"last_login_at"`
}

type Identity struct {
	ID                  string    `json:"id"`
	UserID              string    `json:"user_id"`
	Provider            string    `json:"provider"`
	StudentIDHash       string    `json:"-"`
	StudentAlias        string    `json:"student_alias"`
	StudentIDCiphertext string    `json:"-"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type Session struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	DeviceID       string     `json:"device_id"`
	AccessHash     string     `json:"-"`
	RefreshHash    string     `json:"-"`
	ExpiresAt      time.Time  `json:"expires_at"`
	RefreshExpires time.Time  `json:"refresh_expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     time.Time  `json:"last_used_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
}

type Challenge struct {
	ID            string     `json:"id"`
	DeviceID      string     `json:"device_id"`
	Challenge     string     `json:"-"`
	ChallengeHash string     `json:"-"`
	ExpiresAt     time.Time  `json:"expires_at"`
	UsedAt        *time.Time `json:"used_at,omitempty"`
}

type EventInput struct {
	EventID    string
	UserID     string
	DeviceID   string
	Type       string
	OccurredAt time.Time
	ReceivedAt time.Time
	Properties string
}

type RiskEvent struct {
	ID             string     `json:"id"`
	Type           string     `json:"type"`
	UserID         string     `json:"user_id,omitempty"`
	DeviceID       string     `json:"device_id,omitempty"`
	ObservedCount  int        `json:"observed_count"`
	WindowStart    time.Time  `json:"window_start"`
	WindowEnd      time.Time  `json:"window_end"`
	Detail         string     `json:"detail"`
	CreatedAt      time.Time  `json:"created_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
}

type OAuthClient struct {
	ClientID     string         `json:"client_id"`
	ClientName   string         `json:"client_name"`
	SecretHash   sql.NullString `json:"-"`
	RedirectURIs string         `json:"redirect_uris"`
	Scopes       string         `json:"scopes"`
	Status       string         `json:"status"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type OAuthProvider struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	AuthorizationURL string         `json:"authorization_url"`
	TokenURL         string         `json:"token_url"`
	UserinfoURL      sql.NullString `json:"userinfo_url"`
	ClientID         string         `json:"client_id"`
	SecretCiphertext string         `json:"-"`
	Scopes           string         `json:"scopes"`
	Status           string         `json:"status"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type OAuthAccount struct {
	ID                string         `json:"id"`
	UserID            string         `json:"user_id"`
	ProviderID        string         `json:"provider_id"`
	ExternalSubject   string         `json:"external_subject"`
	DisplayName       string         `json:"display_name"`
	AccessCiphertext  string         `json:"-"`
	RefreshCiphertext sql.NullString `json:"-"`
	ExpiresAt         sql.NullInt64  `json:"expires_at"`
	Scope             string         `json:"scope"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type OAuthTransaction struct {
	ID                     string         `json:"id"`
	UserID                 string         `json:"user_id"`
	DeviceID               sql.NullString `json:"device_id"`
	ProviderID             string         `json:"provider_id"`
	StateHash              string         `json:"-"`
	CodeVerifierCiphertext string         `json:"-"`
	RedirectURI            string         `json:"redirect_uri"`
	ExpiresAt              time.Time      `json:"expires_at"`
	UsedAt                 *time.Time     `json:"used_at,omitempty"`
}

type ServiceClient struct {
	ID               string         `json:"id"`
	Audience         string         `json:"audience"`
	SecretCiphertext sql.NullString `json:"-"`
	Scopes           string         `json:"scopes"`
	Status           string         `json:"status"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type OAuthCode struct {
	CodeHash            string         `json:"-"`
	ClientID            string         `json:"client_id"`
	UserID              string         `json:"user_id"`
	DeviceID            sql.NullString `json:"device_id"`
	RedirectURI         string         `json:"redirect_uri"`
	Scope               string         `json:"scope"`
	CodeChallenge       sql.NullString `json:"-"`
	CodeChallengeMethod sql.NullString `json:"-"`
	ExpiresAt           time.Time      `json:"expires_at"`
	UsedAt              *time.Time     `json:"used_at,omitempty"`
}

type OAuthToken struct {
	TokenHash string         `json:"-"`
	TokenType string         `json:"token_type"`
	ClientID  string         `json:"client_id"`
	UserID    string         `json:"user_id"`
	DeviceID  sql.NullString `json:"device_id"`
	Scope     string         `json:"scope"`
	ExpiresAt time.Time      `json:"expires_at"`
	CreatedAt time.Time      `json:"created_at"`
	RevokedAt *time.Time     `json:"revoked_at,omitempty"`
}

func millis(t time.Time) int64 {
	return t.UTC().UnixMilli()
}

func fromMillis(value int64) time.Time {
	return time.UnixMilli(value).UTC()
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := fromMillis(value.Int64)
	return &t
}
