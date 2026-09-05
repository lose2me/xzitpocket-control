PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    display_name TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS question_banks (
    id TEXT PRIMARY KEY,
    order_id INTEGER NOT NULL UNIQUE CHECK (order_id > 0),
    is_new INTEGER NOT NULL DEFAULT 1 CHECK (is_new IN (0, 1)),
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'draft', 'disabled')),
    requires_cdk INTEGER NOT NULL DEFAULT 0 CHECK (requires_cdk IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS question_bank_id_counter (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    next_number INTEGER NOT NULL CHECK (next_number > 0)
);

INSERT OR IGNORE INTO question_bank_id_counter(id, next_number) VALUES (1, 1);

CREATE TABLE IF NOT EXISTS question_bank_order_counter (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    next_number INTEGER NOT NULL CHECK (next_number > 0)
);

INSERT OR IGNORE INTO question_bank_order_counter(id, next_number) VALUES (1, 1);

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
    password_hash TEXT NOT NULL,
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
    updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT OR IGNORE INTO app_release_config(id, latest_version, download_url, updated_at)
VALUES (1, '', '', 0);

CREATE INDEX IF NOT EXISTS idx_events_time_type ON activity_events(occurred_at, type);
CREATE INDEX IF NOT EXISTS idx_events_user_time ON activity_events(user_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_sessions_user_device ON sessions(user_id, device_id, revoked_at);
CREATE INDEX IF NOT EXISTS idx_risk_time_type ON risk_events(created_at, type);
CREATE INDEX IF NOT EXISTS idx_audit_time ON audit_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_question_banks_order ON question_banks(order_id, id);
CREATE INDEX IF NOT EXISTS idx_questions_bank_order ON questions(bank_id, question_number, sort_order);
CREATE INDEX IF NOT EXISTS idx_question_options_question_order ON question_options(question_id, sort_order);
CREATE INDEX IF NOT EXISTS idx_library_cdks_bank_status ON library_cdks(question_bank_id, status);
CREATE INDEX IF NOT EXISTS idx_library_cdks_bound_student ON library_cdks(bound_student_id_hash, question_bank_id, status);
