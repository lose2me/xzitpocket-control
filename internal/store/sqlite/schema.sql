PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    display_name TEXT NOT NULL DEFAULT '',
    college_name TEXT NOT NULL DEFAULT '',
    class_name TEXT NOT NULL DEFAULT '',
    major_name TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    last_login_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS identities (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    student_id_hash TEXT NOT NULL,
    student_alias TEXT NOT NULL,
    student_id_ciphertext TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(provider, student_id_hash)
);

CREATE TABLE IF NOT EXISTS devices (
    id TEXT PRIMARY KEY,
    device_serial TEXT NOT NULL UNIQUE,
    installation_id TEXT NOT NULL UNIQUE,
    device_token_hash TEXT NOT NULL,
    public_key TEXT NOT NULL,
    platform TEXT NOT NULL,
    app_version TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    revoked_at INTEGER
);

CREATE TABLE IF NOT EXISTS user_devices (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    first_bound_at INTEGER NOT NULL,
    last_login_at INTEGER NOT NULL,
    unbound_at INTEGER,
    PRIMARY KEY(user_id, device_id)
);

CREATE TABLE IF NOT EXISTS auth_challenges (
    id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    challenge_hash TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at INTEGER
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    access_hash TEXT NOT NULL UNIQUE,
    refresh_hash TEXT NOT NULL UNIQUE,
    expires_at INTEGER NOT NULL,
    refresh_expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    revoked_at INTEGER
);

CREATE TABLE IF NOT EXISTS activity_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL,
    user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    occurred_at INTEGER NOT NULL,
    received_at INTEGER NOT NULL,
    properties_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE(device_id, event_id)
);

CREATE TABLE IF NOT EXISTS error_reports (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    student_id_hash TEXT NOT NULL,
    app_version TEXT NOT NULL DEFAULT '',
    platform TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    error_text TEXT NOT NULL DEFAULT '',
    stack_trace TEXT NOT NULL DEFAULT '',
    occurred_at INTEGER NOT NULL,
    received_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS error_report_ignored_students (
    student_id_hash TEXT PRIMARY KEY,
    ignored_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS question_banks (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'draft', 'disabled')),
    requires_cdk INTEGER NOT NULL DEFAULT 0 CHECK (requires_cdk IN (0, 1)),
    course_code TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- A question bank may be shared by several courses. question_banks.course_code
-- keeps the first association for sorting and older clients.
CREATE TABLE IF NOT EXISTS question_bank_courses (
    question_bank_id TEXT NOT NULL REFERENCES question_banks(id) ON DELETE CASCADE,
    course_code TEXT NOT NULL,
    PRIMARY KEY (question_bank_id, course_code)
);

CREATE INDEX IF NOT EXISTS idx_question_bank_courses_code ON question_bank_courses(course_code);

CREATE TABLE IF NOT EXISTS question_bank_id_counter (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    next_number INTEGER NOT NULL CHECK (next_number > 0)
);

INSERT OR IGNORE INTO question_bank_id_counter(id, next_number) VALUES (1, 1);


CREATE TABLE IF NOT EXISTS questions (
    id TEXT PRIMARY KEY,
    bank_id TEXT NOT NULL REFERENCES question_banks(id) ON DELETE CASCADE,
    question_number INTEGER NOT NULL,
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    question_text TEXT NOT NULL,
    correct_answer TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    UNIQUE(bank_id, question_number)
);

CREATE TABLE IF NOT EXISTS question_options (
    id TEXT PRIMARY KEY,
    question_id TEXT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    text TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    UNIQUE(question_id, label)
);

CREATE TABLE IF NOT EXISTS library_cdks (
    id TEXT PRIMARY KEY,
    code_hash TEXT NOT NULL UNIQUE,
    question_bank_id TEXT REFERENCES question_banks(id) ON DELETE RESTRICT,
    bound_student_id_hash TEXT,
    bound_student_id_ciphertext TEXT,
    bound_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'used', 'disabled')),
    created_at INTEGER NOT NULL,
    used_at INTEGER
);

CREATE TABLE IF NOT EXISTS risk_events (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    device_id TEXT REFERENCES devices(id) ON DELETE SET NULL,
    observed_count INTEGER NOT NULL DEFAULT 1,
    window_start INTEGER NOT NULL,
    window_end INTEGER NOT NULL,
    detail_json TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    acknowledged_at INTEGER
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    actor_id TEXT,
    action TEXT NOT NULL,
    target_type TEXT,
    target_id TEXT,
    detail_json TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_users (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    permissions TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    key_ciphertext TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    last_login_at INTEGER NOT NULL,
    disabled_at INTEGER
);

CREATE TABLE IF NOT EXISTS admin_sessions (
    id TEXT PRIMARY KEY,
    admin_id TEXT NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    revoked_at INTEGER
);

-- Singleton public APP release metadata. Values are intentionally separate
-- from process configuration so administrators can update them at runtime.
CREATE TABLE IF NOT EXISTS app_release_config (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    latest_version TEXT NOT NULL DEFAULT '',
    download_url TEXT NOT NULL DEFAULT '',
    changelog TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT OR IGNORE INTO app_release_config(id, latest_version, download_url, changelog, updated_at)
VALUES (1, '', '', '', 0);

-- Singleton TEST APP release configuration. Mirrors app_release_config but is
-- served on separate endpoints so release candidates never reach production
-- clients.
CREATE TABLE IF NOT EXISTS app_release_test_config (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    latest_version TEXT NOT NULL DEFAULT '',
    download_url TEXT NOT NULL DEFAULT '',
    changelog TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT OR IGNORE INTO app_release_test_config(id, latest_version, download_url, changelog, updated_at)
VALUES (1, '', '', '', 0);

-- Singleton public school-calendar configuration managed by the admin console.
-- The payload is stored as normalized JSON so clients can cache it verbatim.
CREATE TABLE IF NOT EXISTS school_calendar_config (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    days_json TEXT NOT NULL DEFAULT '[]',
    slots_json TEXT NOT NULL DEFAULT '[]',
    updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT OR IGNORE INTO school_calendar_config(id, days_json, slots_json, updated_at)
VALUES (1, '[]', '[]', 0);

-- Short-lived encrypted configuration payloads shared between app installs.
CREATE TABLE IF NOT EXISTS share_codes (
    id TEXT PRIMARY KEY,
    code_hash TEXT NOT NULL UNIQUE,
    code_ciphertext TEXT NOT NULL DEFAULT '',
    payload_ciphertext TEXT NOT NULL,
    payload_hash TEXT NOT NULL DEFAULT '',
    created_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_time_type ON activity_events(occurred_at, type);
CREATE INDEX IF NOT EXISTS idx_events_user_time ON activity_events(user_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_error_reports_student_time ON error_reports(student_id_hash, occurred_at);
CREATE INDEX IF NOT EXISTS idx_error_reports_user_time ON error_reports(user_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_sessions_user_device ON sessions(user_id, device_id, revoked_at);
CREATE INDEX IF NOT EXISTS idx_risk_time_type ON risk_events(created_at, type);
CREATE INDEX IF NOT EXISTS idx_audit_time ON audit_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_questions_bank_order ON questions(bank_id, question_number, sort_order);
CREATE INDEX IF NOT EXISTS idx_question_options_question_order ON question_options(question_id, sort_order);
CREATE INDEX IF NOT EXISTS idx_library_cdks_bank_status ON library_cdks(question_bank_id, status);
CREATE INDEX IF NOT EXISTS idx_library_cdks_bound_student ON library_cdks(bound_student_id_hash, question_bank_id, status);
CREATE INDEX IF NOT EXISTS idx_share_codes_expires ON share_codes(expires_at);

-- Courses uploaded by clients at login. One row per course number (code).
CREATE TABLE IF NOT EXISTS courses (
    id TEXT PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Textbooks uploaded with courses. One row per (name, tags) per course; the
-- course association is by course code and is edited manually in the console.
CREATE TABLE IF NOT EXISTS textbooks (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '',
    course_code TEXT NOT NULL DEFAULT '',
    pdf_key TEXT NOT NULL DEFAULT '',
    cover_key TEXT NOT NULL DEFAULT '',
    download_count INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(name, tags, course_code)
);

-- A textbook may be used by several courses. textbooks.course_code keeps the
-- first association for sorting and older clients.
CREATE TABLE IF NOT EXISTS textbook_courses (
    textbook_id TEXT NOT NULL REFERENCES textbooks(id) ON DELETE CASCADE,
    course_code TEXT NOT NULL,
    PRIMARY KEY (textbook_id, course_code)
);

CREATE INDEX IF NOT EXISTS idx_courses_name ON courses(name);
CREATE INDEX IF NOT EXISTS idx_textbooks_name ON textbooks(name);
CREATE INDEX IF NOT EXISTS idx_textbooks_course ON textbooks(course_code);
CREATE INDEX IF NOT EXISTS idx_textbook_courses_code ON textbook_courses(course_code);

-- Resources uploaded with courses. One row per (name, tags) per course; the
-- course association is by course code and is edited manually in the console.
-- Unlike textbooks a resource has no cover, only its PDF.
CREATE TABLE IF NOT EXISTS resources (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '',
    course_code TEXT NOT NULL DEFAULT '',
    pdf_key TEXT NOT NULL DEFAULT '',
    download_count INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(name, tags, course_code)
);

CREATE INDEX IF NOT EXISTS idx_resources_name ON resources(name);
CREATE INDEX IF NOT EXISTS idx_resources_course ON resources(course_code);

-- PDFs uploaded by app users and waiting for administrator review. An
-- approved submission becomes a textbooks/resources row that keeps pdf_key;
-- the row is removed once reviewed.
CREATE TABLE IF NOT EXISTS resource_submissions (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('textbook', 'resource')),
    course_code TEXT NOT NULL,
    name TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '',
    pdf_key TEXT NOT NULL DEFAULT '',
    size_bytes INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending_review' CHECK (status IN ('pending_upload', 'pending_review')),
    submitted_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    submitted_by_name TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_resource_submissions_kind ON resource_submissions(kind, created_at);
CREATE INDEX IF NOT EXISTS idx_resource_submissions_status ON resource_submissions(status, kind, created_at);
