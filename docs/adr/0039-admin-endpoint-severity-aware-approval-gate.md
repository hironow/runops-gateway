# 0039. Admin endpoint severity-aware approval gate

**Date:** 2026-05-10
**Status:** Superseded by ADR 0040 (= JSON-RPC + transport abstraction で再 design)

## Context

ADR 0030 (Accepted) で `/admin/projects` 系 HTTP admin endpoint (= POST add / GET list / GET show / POST archive) を Bearer token auth で公開した。 この経路は production registry mutation の正規 path (ADR 0025 promised) として動作している。

dotfiles ADR 0013 (Status: Proposed) は cdr-project / runops project の lifecycle 操作の severity classification を pin し:

- **HIGH** = `runops project add / archive` / `cdr project up / down` (default soft)
- **HIGH-CRITICAL** = `runops project delete --hard` / `cdr project down --hard`
- **LOW** = `runops project list / show` / `cdr project list / show`

dotfiles ADR 0013 §implementation gate は HIGH 分類されても自動 reject されない gap を明記し、 mitigation の **Option (a) gateway 側で `cmd/runops project create/archive/delete` 実行前に severity-aware approval gate を新設** を推奨 path として指定した。 Option (b) cdr-project wrapper による host 側 gate は「ad-hoc、 client side で gate なので bypass 可能性あり」 と否定評価。

一方、 既存 4-eyes approval orchestration は ADR 0019 (Accepted) + Phase 4a (`internal/usecase/dispatch_result_handler.go` line 94-103) で実装済 = `mail.Kind == convergence` AND `mail.Metadata["severity"] == "high"` を pickup して Slack approve/deny button を post、 別 operator click → `handleApprovalAction` → approval-ack D-Mail publish の経路。 ただし対象は **convergence kind D-Mail のみ**、 admin endpoint mutation 経路は乗っていない。

ADR 0035 (Accepted) で AI agent identity 4-eyes architectural pin、 ADR 0036 で Phase 4a effective_requester_actor_type validation が dispatch / canary deploy + Phase 4a approval path で実装済。 admin endpoint も同じ invariant を適用すべきだが、 現状 approval flow に乗っていないため AI agent identity でも mutation を素通しする。

refs issue 0024 (= 0020-multi follow-up tracker) 軸 B は本 ADR の implementation を要求している。

## Decision

### Option α 採用 (= ADR 0013 §implementation gate Option (a) 推奨整合)

`/admin/projects` 系 endpoint で **POST add / POST archive を severity-aware approval gate に乗せる**。 GET list / GET show は LOW のため approval 不要、 即時 200。

### async two-phase 採用

POST 受信時に長時間 HTTP 接続を hold せず、 **202 Accepted + idempotency_key を即時返却**、 client は polling endpoint で result を取得する。

```
POST /admin/projects { id, github_org, ... }
  → 202 Accepted { idempotency_key, status: "pending_approval" }

POST /admin/projects/{id}/archive
  → 202 Accepted { idempotency_key, status: "pending_approval" }

(別 operator が Slack approve click)

GET /admin/projects/pending/{idempotency_key}
  → 200 { status: "pending_approval" | "approved_applied" | "denied" | "timeout" }
```

### gate flow

1. POST 受信 → Bearer auth + body validation (= 既存)
2. operation severity 判定 (= add / archive = HIGH、 list / show = LOW)
3. if HIGH:
   a. idempotency_key 生成 (= request body SHA-256 prefix or UUID)
   b. **pending state を Firestore に保存** (= ADR 0026 流用、 scale-out + restart-safe):
      - key = idempotency_key
      - op = add | archive
      - body = original request body (snapshot)
      - requester_actor_type = `RUNOPS_ACTOR_TYPE` carry from request env
      - created_at = now
   c. **convergence D-Mail 発行**:
      - source = `runops-gateway-admin`
      - target = `runops-gateway-slack`
      - kind = `convergence`
      - metadata = `{ severity: "high", parent_idempotency_key, op_kind, payload_digest, slack_channel_id, slack_thread_ts, requester_actor_type, initiating_actor_type }`
   d. 既存 ADR 0019 path (`dispatch_result_handler.go`) が D-Mail を pickup → Slack thread に approve/deny button post
   e. HTTP response: 202 Accepted with `{ idempotency_key, status: "pending_approval" }`
4. if LOW: 従来通り即時 mutation + 200
5. 別 operator が Slack approve click → `handleApprovalAction` → approval-ack D-Mail publish (= 既存)
6. **gateway 内 admin approval orchestrator** が approval-ack D-Mail を pickup:
   a. `parent_idempotency_key` で Firestore pending state lookup
   b. ADR 0035 (AI agent cannot approve AI agent) invariant validation = clicker actor type vs requester_actor_type
   c. validation pass → 元 op (add / archive) を Firestore project registry に apply
   d. pending state を `{ status: "approved_applied", applied_at }` に更新
7. denied / timeout の場合 mutation せず pending state を `{ status: "denied" | "timeout" }` に更新
8. client polling: GET `/admin/projects/pending/{key}` で status 取得

### timeout / GC

- approval timeout: **15 分** (= operator 不在対応、 デフォルト)
- timeout 後 pending state を `timeout` 状態に遷移、 元 op は実行しない
- GC: pending state は 7 日後に Firestore から削除 (= operator が後で polling して timeout 確認できるよう猶予)

### idempotency

- 同一 body の POST は同 idempotency_key (= body SHA-256 prefix) で重複検出、 Firestore transaction で create-if-not-exists
- 既存 pending state があれば既 status を return (= retry safe)

## Enforcement inventory

### Entry points

`/admin/projects` 系 mutation を発生させ得る path 全列挙:

1. **operator local Mac**: `curl -H "Authorization: Bearer $RUNOPS_ADMIN_TOKEN" -X POST /admin/projects ...` (= ADR 0030 の day-1 cutover client、 human-operator)
2. **cdr-project wrapper**: workspace VM 内 (= cdr-exec 経由) もしくは operator local Mac から admin endpoint POST (= 0024 軸 B PR §3 で polling logic 追加予定)
3. **AI agent dispatch**: workspace VM 内 cdr-job 経由で admin endpoint POST (= dotfiles ADR 0012 Path B、 `RUNOPS_ACTOR_TYPE=ai-agent`)
4. **将来 Slack dispatch**: `/runops project up <id>` 経由 (= dispatch flow、 別 ADR / 別 PR)

### Persistent / carried data needed at each enforcement point

- HTTP request: `Authorization: Bearer ${RUNOPS_ADMIN_TOKEN}` (= ADR 0030)
- HTTP request: body field 全て (= id / github_org / github_repo / workspace_path / etc.)
- Firestore pending state: idempotency_key / op / body snapshot / requester_actor_type / created_at / status / applied_at
- D-Mail metadata: severity / parent_idempotency_key / op_kind / payload_digest / slack_channel_id / slack_thread_ts / requester_actor_type / initiating_actor_type

### Bypass candidates (= "where can this go wrong?")

1. **AI agent が `RUNOPS_ACTOR_TYPE=human-operator` を override して admin endpoint を叩く**: gateway 側で actor_type を request header / Bearer token claim から取得する設計でない場合、 client side override が通る。 Mitigation: gateway 側で `RUNOPS_ACTOR_TYPE` を **request 経由で受け取らず**、 別経路 (= ADR 0030 admin token に紐付く operator identity) で確定する設計にする。 詳細は本 ADR §Implementation で別途 pin。

2. **convergence D-Mail 発行失敗 → admin endpoint が pending state のまま放置**: D-Mail 発行 failure を gateway 側で観測 → pending state を `error_dmail_publish_failed` に遷移 + retry policy。 単純化のため初期実装では **D-Mail 発行 failure 時 HTTP 5xx を即時返却**、 client retry に任せる (= pending state 作らない)。

3. **同一 idempotency_key で 2 つの POST が race**: Firestore transaction で create-if-not-exists、 両 POST とも同 pending state を return (= 1 つだけ approval flow に乗る、 もう 1 つは既存 state で wait)。

4. **approval-ack D-Mail が複数届く (= retry / duplicate)**: pending state status が既に `approved_applied` なら no-op、 idempotent。

5. **GC 7 日経過後 polling で 404**: client は 7 日以内 polling 推奨、 timeout / error なら 24 時間以内に再 POST 推奨。 GC 後の 404 は client retry を期待する設計。

### Tests proving coverage

各 enforcement point に対して 1 つ以上 test を追加:

1. **severity classification**: `internal/adapter/input/admin/handler_test.go::TestAdminHighSeverityRoutesToApprovalGate` (= POST add / archive で 202 Accepted + idempotency_key)、 `TestAdminLowSeverityImmediateResponse` (= GET list / show で即時 200)
2. **pending state Firestore round-trip**: `internal/adapter/output/firestore/pending_store_test.go::TestPendingStoreCreateIfNotExists` (= idempotency_key で重複検出) + `TestPendingStoreApprovedAppliedTransition`
3. **convergence D-Mail 発行 + approval flow**: `internal/usecase/admin_approval_orchestrator_test.go::TestAdminApprovalOrchestratorPublishesDMail` + `TestAdminApprovalOrchestratorAppliesOpOnAck`
4. **AI agent invariant**: `TestAdminApprovalOrchestratorRejectsAIAgentRequester` (= ADR 0035 carry、 effective_requester_actor_type validation)
5. **timeout**: `TestAdminApprovalOrchestratorTimeoutAfter15Min`
6. **client polling**: `TestAdminPendingPollingReturnsStatus` (= GET /admin/projects/pending/{key})

## Consequences

### Positive

- ADR 0013 §implementation gate Option (a) 推奨 path で gap closure
- AI agent laundering bypass (= ADR 0013 Bypass #3) を server side で root resolve = client (cdr-project / cdr-exec) 経由の actor type override が無効化される
- 既存 ADR 0019 + Phase 4a approval orchestration を 100% 再利用 = approval logic の single source of truth 維持
- async two-phase で HTTP 接続 long hold 不要、 Cloud Run 60 分 timeout に当たらない
- Firestore pending state で scale-out + restart-safe

### Negative

- gateway 側 implementation 重 (= pending state Firestore adapter + approval orchestrator + polling endpoint 追加)
- client (= cdr-project / curl 直叩き operator) 側にも polling logic が必要 (= 既存 admin endpoint 1 回叩きから複数回叩きへ)
- approval timeout 中の operator UX = 待ち時間 (= default 15 分)、 sync hold より体感 latency 高い
- Firestore コスト微増 (= pending state read/write、 ただし 7 日 GC で抑制)

### Neutral

- HTTP middleware で 4-eyes 独立実装する Option β (= approval logic 二重化) は不採用、 approval 経路を ADR 0019 single SoT に統一
- cdr-project 側 approval orchestration する Option γ (= dotfiles ADR 0013 で「ad-hoc、 bypass 可能性あり」 否定評価) は不採用

## Out of scope

- cdr-project ver 2 改修 (= subcommand 名 mismatch / `--workspace` 欠如 fix、 0024 別軸)
- 0024 軸 C (hard-delete 3 種 guard、 別 ADR / 別 PR)
- 0024 軸 D (AI agent forbidden path test)
- 0024 軸 A (full E2E integration test)
- HTTP middleware 4-eyes 独立実装 (= Option β、 不採用)
- cdr-project 側 approval orchestration (= Option γ、 不採用)
- HTTP sync hold (= 不採用)
- Slack `/runops project up` dispatch path (= 別 ADR / 別 PR)

## References

- [refs/HTMLification/docs/issues/0024-multi-project-lifecycle-followup.html](../../../tap/refs/HTMLification/docs/issues/0024-multi-project-lifecycle-followup.html) — parent issue 軸 B
- [dotfiles/docs/adr/0013-project-lifecycle-severity-classification.md](../../../dotfiles/docs/adr/0013-project-lifecycle-severity-classification.md) — severity policy pin (Status: Proposed)、 §implementation gate Option (a) 推奨 path
- ADR 0019 — HIGH severity D-Mail Slack 4-eyes approval (本 ADR の前提 path)
- ADR 0026 — Firestore production deploy (本 ADR の pending state storage)
- ADR 0030 — HTTP admin endpoint authentication (本 ADR が拡張する経路)
- ADR 0035 — AI agent cannot approve AI agent (本 ADR の architectural pin)
- ADR 0036 — Phase 4a approval actor-type validation (本 ADR の effective_requester_actor_type carry)
- `internal/usecase/dispatch_result_handler.go` — 既存 ADR 0019 + Phase 4a impl
- `internal/adapter/input/admin/handler.go` — 既存 admin endpoint (本 ADR で拡張)
