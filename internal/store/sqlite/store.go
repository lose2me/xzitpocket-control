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
	ID                  string
	UserID              string
	Provider            string
	StudentIDHash       string
	StudentAlias        string
	StudentIDCiphertext string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type Session struct {
	ID             string
	UserID         string
	DeviceID       string
	AccessHash     string
	RefreshHash    string
	ExpiresAt      time.Time
	RefreshExpires time.Time
	CreatedAt      time.Time
	LastUsedAt     time.Time
	RevokedAt      *time.Time
}

type Challenge struct {
	ID            string
	DeviceID      string
	Challenge     string
	ChallengeHash string
	ExpiresAt     time.Time
	UsedAt        *time.Time
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
	ClientID     string
	ClientName   string
	SecretHash   sql.NullString
	RedirectURIs string
	Scopes       string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type OAuthProvider struct {
	ID               string
	Name             string
	AuthorizationURL string
	TokenURL         string
	UserinfoURL      sql.NullString
	ClientID         string
	SecretCiphertext string
	Scopes           string
	Status           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type OAuthAccount struct {
	ID                string
	UserID            string
	ProviderID        string
	ExternalSubject   string
	DisplayName       string
	AccessCiphertext  string
	RefreshCiphertext sql.NullString
	ExpiresAt         sql.NullInt64
	Scope             string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type OAuthTransaction struct {
	ID                     string
	UserID                 string
	DeviceID               sql.NullString
	ProviderID             string
	StateHash              string
	CodeVerifierCiphertext string
	RedirectURI            string
	ExpiresAt              time.Time
	UsedAt                 *time.Time
}

type ServiceClient struct {
	ID               string
	Audience         string
	SecretCiphertext sql.NullString
	Scopes           string
	Status           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type OAuthCode struct {
	CodeHash            string
	ClientID            string
	UserID              string
	DeviceID            sql.NullString
	RedirectURI         string
	Scope               string
	CodeChallenge       sql.NullString
	CodeChallengeMethod sql.NullString
	ExpiresAt           time.Time
	UsedAt              *time.Time
}

type OAuthToken struct {
	TokenHash string
	TokenType string
	ClientID  string
	UserID    string
	DeviceID  sql.NullString
	Scope     string
	ExpiresAt time.Time
	CreatedAt time.Time
	RevokedAt *time.Time
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
