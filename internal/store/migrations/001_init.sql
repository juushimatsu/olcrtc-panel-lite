CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_users (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id_hash TEXT PRIMARY KEY,
    admin_id INTEGER NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    csrf_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    ip TEXT NOT NULL,
    user_agent TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_expires_idx ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS instances (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    provider TEXT NOT NULL,
    transport TEXT NOT NULL,
    room_id TEXT NOT NULL,
    room_channel TEXT NOT NULL DEFAULT '',
    auth_token_encrypted TEXT NOT NULL DEFAULT '',
    dns TEXT NOT NULL,
    outbound_proxy_encrypted TEXT NOT NULL DEFAULT '',
    transport_options_json TEXT NOT NULL,
    liveness_json TEXT NOT NULL,
    max_session_duration TEXT NOT NULL DEFAULT '',
    traffic_options_json TEXT NOT NULL,
    debug INTEGER NOT NULL DEFAULT 0,
    reset_policy TEXT NOT NULL DEFAULT 'never',
    quota_bytes INTEGER NOT NULL DEFAULT 0,
    expires_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS traffic_counters (
    instance_id INTEGER PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
    upload_bytes INTEGER NOT NULL DEFAULT 0,
    download_bytes INTEGER NOT NULL DEFAULT 0,
    total_bytes INTEGER NOT NULL DEFAULT 0,
    period_started_at TEXT NOT NULL,
    last_event_at TEXT,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS traffic_events (
    journal_cursor TEXT PRIMARY KEY,
    instance_id INTEGER NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    target_hash TEXT NOT NULL,
    upload_bytes INTEGER NOT NULL,
    download_bytes INTEGER NOT NULL,
    event_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    refresh_interval TEXT NOT NULL DEFAULT '10m',
    color TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    mirror_enabled INTEGER NOT NULL DEFAULT 0,
    mirror_key_encrypted TEXT NOT NULL DEFAULT '',
    mirror_public_url TEXT NOT NULL DEFAULT '',
    mirror_status TEXT NOT NULL DEFAULT 'disabled',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS subscription_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    source_instance_id INTEGER REFERENCES instances(id) ON DELETE CASCADE,
    raw_uri TEXT,
    exclave_compatible INTEGER NOT NULL DEFAULT 0,
    name TEXT NOT NULL DEFAULT '',
    color TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT '',
    ip TEXT NOT NULL DEFAULT '',
    comment TEXT NOT NULL DEFAULT '',
    manual_used INTEGER,
    manual_available INTEGER,
    expires_at TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK ((source_instance_id IS NOT NULL AND raw_uri IS NULL) OR (source_instance_id IS NULL AND raw_uri IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS subscription_entries_order_idx ON subscription_entries(subscription_id, sort_order, id);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    encrypted INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    action TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL DEFAULT '',
    result TEXT NOT NULL,
    actor_ip TEXT NOT NULL DEFAULT '',
    details_redacted TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_created_idx ON audit_log(created_at DESC);

CREATE TABLE IF NOT EXISTS releases (
    bundle_id TEXT PRIMARY KEY,
    panel_version TEXT NOT NULL,
    upstream_sha TEXT NOT NULL,
    upstream_commit_time TEXT NOT NULL,
    installed_at TEXT NOT NULL,
    state TEXT NOT NULL,
    path TEXT NOT NULL,
    checksum TEXT NOT NULL
);
