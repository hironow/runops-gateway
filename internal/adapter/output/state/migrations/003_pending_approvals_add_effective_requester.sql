-- ADR 0040 §B-5 — extend pending_approvals with effective_requester_id so
-- the admin approval orchestrator can enforce the 4-eyes invariant
-- (ADR 0035 carry) at approval-ack time.
--
-- Existing rows from ADR 0039 era will have an empty string in this
-- column; the orchestrator treats that as "unresolved requester" and
-- fails closed (= rejects the apply step).
ALTER TABLE pending_approvals
    ADD COLUMN effective_requester_id TEXT NOT NULL DEFAULT '';
