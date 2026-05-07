CREATE TABLE IF NOT EXISTS _migrations (
    id          TEXT PRIMARY KEY NOT NULL,
    applied_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS projects (
    id                          TEXT PRIMARY KEY NOT NULL,
    github_org                  TEXT NOT NULL,
    github_repo                 TEXT NOT NULL,
    workspace_path              TEXT NOT NULL,
    slack_default_channel       TEXT NOT NULL DEFAULT '',
    github_app_installation_id  INTEGER NOT NULL DEFAULT 0,
    status                      TEXT NOT NULL DEFAULT 'active'
                                    CHECK (status IN ('active', 'archived')),
    created_at                  TEXT NOT NULL DEFAULT (datetime('now')),
    archived_at                 TEXT
);

CREATE INDEX IF NOT EXISTS idx_projects_status ON projects(status);
