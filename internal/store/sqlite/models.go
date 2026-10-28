package sqlite

import (
	"database/sql"
	"time"
)

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
	Name        string
	Status      string
	RequiresCDK bool
	CourseCode  string
	CourseCodes []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ArchivedAt  time.Time
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
	Name          string
	Status        string
	RequiresCDK   bool
	CourseCode    string
	CourseCodes   []string
	QuestionCount int
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ArchivedAt    time.Time
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

// optionalMillis stores a zero time as 0 so "not archived" checks stay simple.
func optionalMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return millis(t)
}

func optionalFromMillis(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return fromMillis(value)
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := fromMillis(value.Int64)
	return &t
}
