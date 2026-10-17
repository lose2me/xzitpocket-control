package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var embeddedSchema string

var errSchemaMismatch = errors.New("database schema does not match the current format")

var currentSchemaTables = []struct {
	name    string
	columns []string
}{
	{name: "users", columns: []string{"id", "status", "display_name", "college_name", "class_name", "major_name", "created_at", "last_login_at"}},
	{name: "identities", columns: []string{"id", "user_id", "provider", "student_id_hash", "student_alias", "student_id_ciphertext", "created_at", "updated_at"}},
	{name: "devices", columns: []string{"id", "device_serial", "installation_id", "device_token_hash", "public_key", "platform", "app_version", "created_at", "last_seen_at", "revoked_at"}},
	{name: "user_devices", columns: []string{"user_id", "device_id", "first_bound_at", "last_login_at", "unbound_at"}},
	{name: "auth_challenges", columns: []string{"id", "device_id", "challenge_hash", "expires_at", "used_at"}},
	{name: "sessions", columns: []string{"id", "user_id", "device_id", "access_hash", "refresh_hash", "expires_at", "refresh_expires_at", "created_at", "last_used_at", "revoked_at"}},
	{name: "activity_events", columns: []string{"id", "event_id", "user_id", "device_id", "type", "occurred_at", "received_at", "properties_json"}},
	{name: "error_reports", columns: []string{"id", "event_id", "user_id", "device_id", "student_id_hash", "app_version", "platform", "title", "message", "error_text", "stack_trace", "occurred_at", "received_at"}},
	{name: "error_report_ignored_students", columns: []string{"student_id_hash", "ignored_at"}},
	{name: "question_banks", columns: []string{"id", "order_id", "is_new", "name", "status", "requires_cdk", "course_code", "created_at", "updated_at"}},
	{name: "question_bank_id_counter", columns: []string{"id", "next_number"}},
	{name: "question_bank_order_counter", columns: []string{"id", "next_number"}},
	{name: "questions", columns: []string{"id", "bank_id", "question_number", "type", "title", "question_text", "correct_answer", "sort_order"}},
	{name: "question_options", columns: []string{"id", "question_id", "label", "text", "sort_order"}},
	{name: "library_cdks", columns: []string{"id", "code_hash", "question_bank_id", "bound_student_id_hash", "bound_student_id_ciphertext", "bound_user_id", "status", "created_at", "used_at"}},
	{name: "risk_events", columns: []string{"id", "type", "user_id", "device_id", "observed_count", "window_start", "window_end", "detail_json", "created_at", "acknowledged_at"}},
	{name: "audit_logs", columns: []string{"id", "actor_id", "action", "target_type", "target_id", "detail_json", "created_at"}},
	{name: "admin_users", columns: []string{"id", "name", "permissions", "password_hash", "key_ciphertext", "created_at", "last_login_at", "disabled_at"}},
	{name: "admin_sessions", columns: []string{"id", "admin_id", "token_hash", "expires_at", "created_at", "last_used_at", "revoked_at"}},
	{name: "app_release_config", columns: []string{"id", "latest_version", "download_url", "changelog", "updated_at"}},
	{name: "app_release_test_config", columns: []string{"id", "latest_version", "download_url", "changelog", "updated_at"}},
	{name: "school_calendar_config", columns: []string{"id", "days_json", "slots_json", "updated_at"}},
	{name: "share_codes", columns: []string{"id", "code_hash", "code_ciphertext", "payload_ciphertext", "payload_hash", "created_by_user_id", "created_at", "expires_at"}},
	{name: "courses", columns: []string{"id", "code", "name", "created_at", "updated_at"}},
	{name: "textbooks", columns: []string{"id", "name", "tags", "course_code", "pdf_key", "cover_key", "created_at", "updated_at"}},
	{name: "resources", columns: []string{"id", "name", "tags", "course_code", "pdf_key", "created_at", "updated_at"}},
}

type Store struct {
	DB   *sql.DB
	Path string

	// lastResetBackup is the path of the crash snapshot written the last time an
	// incompatible database was replaced, empty when no reset happened.
	lastResetBackup string

	// lastMigrationBackup is the path of the snapshot written before schema
	// columns were removed, empty when no destructive migration happened.
	lastMigrationBackup string
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

// additiveMigrations lists columns introduced after the initial deployment.
// They are ALTERed in place BEFORE schema validation so an existing production
// database keeps its data instead of failing validation and being rebuilt.
var additiveMigrations = []struct {
	table  string
	column string
	ddl    string
}{
	{"app_release_config", "changelog", `ALTER TABLE app_release_config ADD COLUMN changelog TEXT NOT NULL DEFAULT ''`},
	{"school_calendar_config", "slots_json", `ALTER TABLE school_calendar_config ADD COLUMN slots_json TEXT NOT NULL DEFAULT '[]'`},
	{"users", "major_name", `ALTER TABLE users ADD COLUMN major_name TEXT NOT NULL DEFAULT ''`},
	{"textbooks", "pdf_key", `ALTER TABLE textbooks ADD COLUMN pdf_key TEXT NOT NULL DEFAULT ''`},
	{"textbooks", "cover_key", `ALTER TABLE textbooks ADD COLUMN cover_key TEXT NOT NULL DEFAULT ''`},
	{"admin_users", "name", `ALTER TABLE admin_users ADD COLUMN name TEXT NOT NULL DEFAULT ''`},
	{"admin_users", "permissions", `ALTER TABLE admin_users ADD COLUMN permissions TEXT NOT NULL DEFAULT ''`},
	{"question_banks", "course_code", `ALTER TABLE question_banks ADD COLUMN course_code TEXT NOT NULL DEFAULT ''`},
	{"admin_users", "key_ciphertext", `ALTER TABLE admin_users ADD COLUMN key_ciphertext TEXT NOT NULL DEFAULT ''`},
}

func (s *Store) applyAdditiveMigrations(ctx context.Context) error {
	for _, migration := range additiveMigrations {
		rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info("`+migration.table+`")`)
		if err != nil {
			return err
		}
		// A fresh database has no table yet; CREATE TABLE below adds the column
		// directly, so there is nothing to ALTER.
		tableExists := false
		found := false
		for rows.Next() {
			tableExists = true
			var cid, notNull, primaryKey int
			var name, dataType string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
				_ = rows.Close()
				return err
			}
			if name == migration.column {
				found = true
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if tableExists && !found {
			if _, err := s.DB.ExecContext(ctx, migration.ddl); err != nil {
				return fmt.Errorf("add %s.%s: %w", migration.table, migration.column, err)
			}
		}
	}
	return nil
}

// removedColumns lists columns dropped from the schema after the initial
// deployment. They are dropped before validation so an existing production
// database keeps its other data instead of failing validation and being wiped.
// A snapshot is written before the first change so an operator can recover.
var removedColumns = []struct {
	table  string
	column string
}{
	{"courses", "books"},
}

func (s *Store) applyColumnRemovals(ctx context.Context) error {
	for _, removal := range removedColumns {
		found, err := s.tableHasColumn(ctx, removal.table, removal.column)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		if s.lastMigrationBackup == "" {
			backup, backupErr := s.backupIncompatibleDatabase(ctx)
			if backupErr != nil {
				return fmt.Errorf("snapshot before removing %s.%s: %w", removal.table, removal.column, backupErr)
			}
			s.lastMigrationBackup = backup
		}
		if _, err := s.DB.ExecContext(ctx, `ALTER TABLE "`+removal.table+`" DROP COLUMN "`+removal.column+`"`); err != nil {
			return fmt.Errorf("remove %s.%s: %w", removal.table, removal.column, err)
		}
	}
	return nil
}

func (s *Store) tableHasColumn(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) Migrate(ctx context.Context) error {
	// Additive column migrations must run BEFORE the embedded schema: the
	// schema's singleton INSERTs already reference the new columns, and an
	// old database would fail on them.
	if err := s.applyAdditiveMigrations(ctx); err != nil {
		return fmt.Errorf("apply additive migrations: %w", err)
	}
	if err := s.applyColumnRemovals(ctx); err != nil {
		return fmt.Errorf("apply column removals: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, embeddedSchema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if err := s.validateCurrentSchema(ctx); err != nil {
		if !errors.Is(err, errSchemaMismatch) {
			return fmt.Errorf("validate schema: %w", err)
		}
		backup, backupErr := s.backupIncompatibleDatabase(ctx)
		if backupErr != nil {
			return fmt.Errorf("backup incompatible database: %w", backupErr)
		}
		s.lastResetBackup = backup
		if err := s.resetDatabase(ctx); err != nil {
			return fmt.Errorf("reset incompatible database: %w", err)
		}
		if _, err := s.DB.ExecContext(ctx, embeddedSchema); err != nil {
			return fmt.Errorf("rebuild schema: %w", err)
		}
		if err := s.validateCurrentSchema(ctx); err != nil {
			return fmt.Errorf("validate rebuilt schema: %w", err)
		}
	}
	return nil
}

func (s *Store) validateCurrentSchema(ctx context.Context) error {
	expectedTables := make(map[string]struct{}, len(currentSchemaTables))
	for _, table := range currentSchemaTables {
		expectedTables[table.name] = struct{}{}
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return err
	}
	actualTables := make(map[string]struct{}, len(expectedTables))
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		actualTables[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(actualTables) != len(expectedTables) {
		return fmt.Errorf("%w: table set differs", errSchemaMismatch)
	}
	for name := range actualTables {
		if _, ok := expectedTables[name]; !ok {
			return fmt.Errorf("%w: unexpected table %s", errSchemaMismatch, name)
		}
	}

	for _, table := range currentSchemaTables {
		rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info("`+table.name+`")`)
		if err != nil {
			return err
		}
		columns := make(map[string]struct{}, len(table.columns))
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, dataType string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
				_ = rows.Close()
				return err
			}
			columns[name] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(columns) != len(table.columns) {
			return fmt.Errorf("%w: columns in %s differ", errSchemaMismatch, table.name)
		}
		for _, column := range table.columns {
			if _, ok := columns[column]; !ok {
				return fmt.Errorf("%w: missing %s.%s", errSchemaMismatch, table.name, column)
			}
		}
	}
	return nil
}

// LastResetBackup returns the path of the crash snapshot taken before an
// incompatible database was wiped, or "" when no reset occurred.
func (s *Store) LastResetBackup() string { return s.lastResetBackup }

// LastMigrationBackup returns the path of the snapshot taken before schema
// columns were removed, or "" when no destructive migration occurred.
func (s *Store) LastMigrationBackup() string { return s.lastMigrationBackup }

// backupIncompatibleDatabase writes a consistent copy of the current database
// next to it, prefixed with "crash-", before an incompatible schema is dropped.
// VACUUM INTO includes any pending WAL content, so the snapshot is complete.
func (s *Store) backupIncompatibleDatabase(ctx context.Context) (string, error) {
	dir := filepath.Dir(s.Path)
	if dir == "" {
		dir = "."
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	dest := filepath.Join(dir, "crash-"+stamp+".db")
	for i := 1; ; i++ {
		if _, err := os.Stat(dest); errors.Is(err, os.ErrNotExist) {
			break
		}
		dest = filepath.Join(dir, fmt.Sprintf("crash-%s-%d.db", stamp, i))
	}
	statement := "VACUUM INTO '" + strings.ReplaceAll(dest, "'", "''") + "'"
	if _, err := s.DB.ExecContext(ctx, statement); err != nil {
		return "", fmt.Errorf("snapshot database to %s: %w", dest, err)
	}
	return dest, nil
}

func (s *Store) resetDatabase(ctx context.Context) error {
	type schemaObject struct {
		kind string
		name string
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT type, name FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' AND type IN ('index', 'trigger', 'view', 'table')
		ORDER BY CASE type WHEN 'index' THEN 1 WHEN 'trigger' THEN 2 WHEN 'view' THEN 3 ELSE 4 END, name`)
	if err != nil {
		return err
	}
	objects := make([]schemaObject, 0)
	for rows.Next() {
		var object schemaObject
		if err := rows.Scan(&object.kind, &object.name); err != nil {
			_ = rows.Close()
			return err
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer func() { _, _ = s.DB.ExecContext(context.Background(), "PRAGMA foreign_keys = ON") }()
	for _, object := range objects {
		statement := "DROP " + strings.ToUpper(object.kind) + " IF EXISTS \"" + strings.ReplaceAll(object.name, `"`, `""`) + `"`
		if _, err := s.DB.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("drop %s %s: %w", object.kind, object.name, err)
		}
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
	CollegeName string    `json:"college_name"`
	ClassName   string    `json:"class_name"`
	MajorName   string    `json:"major_name"`
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

// QuestionBank is the normalized question library aggregate.
type QuestionBank struct {
	ID          string
	OrderID     int
	IsNew       bool
	Name        string
	Status      string
	RequiresCDK bool
	CourseCode  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Questions   []Question
}

type Question struct {
	ID             string
	BankID         string
	QuestionNumber int
	Type           string
	Title          string
	QuestionText   string
	CorrectAnswer  string
	SortOrder      int
	Options        []QuestionOption
}

type QuestionOption struct {
	ID         string
	QuestionID string
	Label      string
	Text       string
	SortOrder  int
}

type QuestionBankSummary struct {
	ID            string
	OrderID       int
	IsNew         bool
	Name          string
	Status        string
	RequiresCDK   bool
	CourseCode    string
	QuestionCount int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// LibraryCDK is an unassigned code that binds to one question bank on redemption.
// The plaintext code is deliberately never stored and is only returned by the create call.
type LibraryCDK struct {
	ID                       string
	QuestionBankID           string
	CodeHash                 string
	BoundStudentIDHash       string
	BoundStudentIDCiphertext string
	BoundUserID              string
	Status                   string
	CreatedAt                time.Time
	UsedAt                   *time.Time
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
	ID               string     `json:"id"`
	Type             string     `json:"type"`
	UserID           string     `json:"user_id,omitempty"`
	DisplayName      string     `json:"display_name,omitempty"`
	ClassName        string     `json:"class_name,omitempty"`
	DeviceID         string     `json:"device_id,omitempty"`
	DeviceAppVersion string     `json:"app_version,omitempty"`
	DeviceRevokedAt  *time.Time `json:"device_revoked_at,omitempty"`
	ObservedCount    int        `json:"observed_count"`
	WindowStart      time.Time  `json:"window_start"`
	WindowEnd        time.Time  `json:"window_end"`
	Detail           string     `json:"detail"`
	CreatedAt        time.Time  `json:"created_at"`
	AcknowledgedAt   *time.Time `json:"acknowledged_at,omitempty"`
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
