CREATE TABLE IF NOT EXISTS pending_approvals (
    idempotency_key       TEXT PRIMARY KEY NOT NULL,
    op                    TEXT NOT NULL CHECK (op IN ('add', 'archive')),
    body_json             BLOB NOT NULL,
    requester_actor_type  TEXT NOT NULL,
    created_at            TEXT NOT NULL DEFAULT (datetime('now')),
    status                TEXT NOT NULL DEFAULT 'pending_approval'
                              CHECK (status IN (
                                  'pending_approval',
                                  'approved_applied',
                                  'denied',
                                  'timeout'
                              )),
    applied_at            TEXT
);

CREATE INDEX IF NOT EXISTS idx_pending_approvals_status ON pending_approvals(status);
CREATE INDEX IF NOT EXISTS idx_pending_approvals_created ON pending_approvals(created_at);
