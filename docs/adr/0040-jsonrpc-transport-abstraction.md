# 0040. JSON-RPC + transport abstraction for admin endpoint approval gate

**Date:** 2026-05-10
**Status:** Proposed (supersedes ADR 0039)

## Context

ADR 0039 (Proposed) に対する設計 review で、 admin endpoint の HIGH severity approval gate を **JSON-RPC + transport 抽象化** で再 design する方針に切り替わった。 user 指示:

> 「endpoint だけど、 JSON-RPC にしてくれないかな (REST API にしたくない)」
> 「拡張性という意味で JSON RPC がいいと思っている。 transport 層を設けることで http 以外に websocket や webrtc にも将来的に対応できる余地を用意したい」

切替の動機:

- **transport 非依存**: JSON-RPC envelope は method + params + id だけで成立、 HTTP / WebSocket / WebRTC を carry 可能。 REST は HTTP method + URL path に依存し、 transport 移行に再 design が必要。
- **拡張性**: 新 method 追加が method 名追加だけで済む、 URL routing / status code 解釈設計が不要。
- **approval gate との適合**: HTTP 202 status code に依存せず、 result envelope 内 `status` field で表現可能 (= transport-agnostic)。
- **client 側 generic dispatcher**: 単一 dispatch loop で全 method 処理、 method ごとの URL / status / body 形状分解不要。

加えて ADR 0039 の REST extension は以下の bypass を残していた (= codex review で検出):

1. 既存 REST `POST /admin/projects` / `POST /admin/projects/{id}/archive` が同 token で即時 mutation 可能 (= approval gate bypass path)
2. 単一 `RUNOPS_ADMIN_TOKEN` (ADR 0030) では requester identity を確定できず、 `effective_requester_id != approver_id` 検証 (= ADR 0035 4-eyes invariant) が成立しない

本 ADR は ADR 0039 を supersede し、 上記を含めて再 design する。

## Decision

### §JSON-RPC 2.0 spec 準拠

- envelope: `{jsonrpc: "2.0", method: string, params: object|array, id: string|number|null}`
- response: `{jsonrpc: "2.0", result: any, id: ...}` or `{jsonrpc: "2.0", error: {code, message, data?}, id: ...}`
- notification (= `id` 不在) は admin endpoint では **不採用** (= mutation で response 必須、 notification 受信は JSON-RPC error envelope `CodeInvalidRequest` -32600 で reject、 HTTP 層は §HTTP transport の `200 OK always` に従い envelope 内 error として表現)
- batch request は **Phase 1 で不採用** (= 複雑度 / approval gate との相互作用回避、 future PR で検討)
- error code は spec 準拠 (= -32700 parse, -32600 invalid request, -32601 method not found, -32602 invalid params, -32603 internal error) + 独自 application error は -32000 〜 -32099 の reserved range で定義

### §transport abstraction

```go
package rpc

// Transport は JSON-RPC envelope の入出力を担う。 具象実装: HTTP / WebSocket / WebRTC。
// Phase 1 で HTTP 1 つだけ実装、 WS/WebRTC は interface 公開のみ (= future PR で具象追加)。
type Transport interface {
    // ServeRPC は incoming JSON-RPC envelope (= raw JSON bytes) を受け取り、
    // dispatcher に委譲、 response envelope を encode して返す。
    ServeRPC(ctx context.Context, in []byte) (out []byte, err error)
}

// Dispatcher は method 名 → Method 実装の routing を行う。
type Dispatcher struct {
    methods map[string]Method
}

// Method は 1 つの JSON-RPC method の handler。
type Method interface {
    Name() string  // 例: "runops.admin.project.add"
    Handle(ctx context.Context, params json.RawMessage) (any, *Error)
}

// Error は JSON-RPC 2.0 error object。
type Error struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Data    any    `json:"data,omitempty"`
}
```

### §HTTP transport (Phase 1)

- POST `/rpc` 単一 endpoint (= REST `/admin/*` とは別 path、 並行運用)
- Body: JSON-RPC 2.0 envelope (= `Content-Type: application/json`)
- HTTP status の二層分離:
  - **transport-layer reject** (= dispatcher 到達前) は HTTP status を返す:
    - `405 Method Not Allowed`: GET 等 POST 以外
    - `415 Unsupported Media Type`: 非 application/json
    - `401 Unauthorized`: Authorization header 不在 / malformed / token registry miss
  - **JSON-RPC layer 到達後** (= parse error / unknown method / handler error / handler success) は **200 OK + envelope** で返す
  - **server-internal failure** (= 500) のみ envelope を出さず raw text
- Auth: `RUNOPS_ADMIN_TOKENS_REGISTRY_FILE` で multi-token (= §identity contract 参照)

### §method 命名規約

namespace: `runops.<service>.<resource>.<verb>`

| method | severity | params | result |
|---|---|---|---|
| `runops.admin.project.add` | HIGH | `{id, github_org, github_repo, workspace_path, slack_default_channel?, github_app_installation_id?}` | `{idempotency_key, status: "pending_approval"}` |
| `runops.admin.project.archive` | HIGH | `{id}` | `{idempotency_key, status: "pending_approval"}` |
| `runops.admin.project.get` | LOW | `{id}` | `{project: Project}` |
| `runops.admin.project.list` | LOW | `{status?: "active"\|"archived"\|"all"}` | `{projects: [Project]}` |
| `runops.admin.project.pending.get` | LOW | `{idempotency_key}` | `{idempotency_key, status, op, applied_at?}` |

### §identity contract

ADR 0030 単一 `RUNOPS_ADMIN_TOKEN` では requester identity 不確定。 本 ADR は **multi-admin-token registry** で per-operator identity を確定する:

`RUNOPS_ADMIN_TOKENS_REGISTRY_FILE` (= file path、 default = unset):

```yaml
# /etc/runops/admin-tokens.yaml (root:root 0600)
tokens:
  - operator_id: U01234ABCD       # Slack user_id (= 4-eyes namespace 一致)
    token_hash: <sha256-hex>      # token は record しない、 hash のみ
    email: alice@example.com      # log/audit 用
  - operator_id: U05678EFGH
    token_hash: <sha256-hex>
    email: bob@example.com
```

`/rpc` 受信時:

1. Bearer token を `Authorization` header から strict 解析 (= ADR 0030 §4 carry: no TrimSpace、 single space separator、 control char reject)
2. token を SHA-256 hash → registry lookup
3. hit → `effective_requester_id = operator_id` 確定 + `requester_actor_type = "human-operator"` 固定 (= admin token は human-bound、 AI agent path は別 endpoint)
4. miss → 401 unauthorized

token lookup strategy: SHA256(submitted_token) を 64 char hex に変換、 registry の `map[hex]Operator` で O(1) lookup。 strict constant-time scan は採用しない (= network latency が dominant で、 map non-constant-time の余地は実害無視可能)。

`RUNOPS_ADMIN_TOKENS_REGISTRY_FILE` 不在時:

- HIGH mutation method (= add / archive) は **flag に関係なく block** (= identity 不確定で HIGH 操作不可、 fail-closed)
- read-only method は legacy `RUNOPS_ADMIN_TOKEN` (= ADR 0030) で fallback 動作

### §4-eyes invariant (= ADR 0035 carry)

- `effective_requester_id` (= JSON-RPC token holder operator_id from registry)
- `approver_id` (= Slack approve click user_id、 既存 ADR 0019 + Phase 4a carry)
- 両者は **同一 Slack user_id namespace**
- approval-ack 受信時 `effective_requester_id != approver_id` を validate、 等しければ reject (= self-approval 禁止、 ADR 0035 invariant)

### §approval gate integration

`runops.admin.project.add` / `archive` 受信時:

1. severity 判定 (= ADR 0013 carry: add / archive = HIGH)
2. `RUNOPS_RPC_HIGH_MUTATION_ENABLED` flag check (= default off、 production ready 時 on)
3. flag off → JSON-RPC error (-32000 application error) 「HIGH mutation disabled」 で reject
4. flag on:
   a. idempotency_key 生成 (= SHA-256 prefix of `effective_requester_id || method || params`)
   b. domain.PendingApproval を CreateIfNotExists (= §2-a domain + §2-b/§2-c adapter 既実装、 transport 非依存で再利用)
   c. JSON-RPC result `{idempotency_key, status: "pending_approval"}` を返す
5. orchestrator (= 別 component) が convergence D-Mail を publish (= 既存 ADR 0019 + Phase 4a path)
6. 別 operator が Slack approve click → approval-ack D-Mail
7. orchestrator が approval-ack pickup → `effective_requester_id != approver_id` validate → Firestore registry に apply → pending state を `approved_applied` に遷移
8. client polling: `runops.admin.project.pending.get` で status 取得

### §REST endpoint との関係 (= ADR 0030 partial supersede)

- 既存 REST `/admin/projects` の **read-only** (= GET list / GET show) は **keep**
- 既存 REST **write** (= POST /admin/projects、 POST /admin/projects/{id}/archive) は:
  - `RUNOPS_RPC_HIGH_MUTATION_ENABLED=1` 時 = **disable** (= 410 Gone + Location: /rpc 案内、 approval gate bypass 完全閉鎖)
  - flag off (= default、 development) = legacy 挙動 keep
- ADR 0030 は **partial supersede** (= REST write only、 read-only は keep)
- migration plan は別 plan scope (= future、 REST read-only も deprecate するなら別 PR)

### §implementation roadmap (= sub PR sequence)

| sub PR | scope | flag default |
|---|---|---|
| §B-1 (本 ADR) | ADR 0040 起案 + ADR 0039 supersede declaration | — |
| §B-2 | transport interface + dispatcher + envelope encode/decode | off |
| §B-3 | HTTP transport 具象 + multi-token registry parser | off |
| §B-4 | project read-only methods (= get / list / pending.get) のみ register、 mutation 不在 | off |
| §B-5 | project mutation methods (= add / archive) + admin approval orchestrator + REST write disable + flag default on 検討 | off / production decision |

§B-4 までは feature flag `RUNOPS_RPC_ENDPOINT_ENABLED` で off default、 dev/test で flag on。 §B-5 で mutation method + orchestrator が atomic に landed = pending stuck 解消。

### §future transport (= scope 外、 後続 PR)

- WebSocket transport: persistent connection で push notification (= approval-ack 通知を server → client push、 polling 不要化)
- WebRTC transport: P2P / browser direct (= operator UI tooling)
- いずれも本 ADR の `Transport` interface に対する具象実装、 dispatcher / method 構造は変更不要

## Enforcement inventory

### Entry points

- POST `/rpc` (= JSON-RPC、 本 ADR で新設、 multi-token registry auth)
- POST / GET `/admin/*` (= legacy REST、 ADR 0030)、 write は flag on で disable

### Persistent / carried data needed at each enforcement point

- HTTP request: `Authorization: Bearer <token>`
- Multi-token registry: `RUNOPS_ADMIN_TOKENS_REGISTRY_FILE` の operator_id ↔ token_hash mapping
- Firestore pending state: idempotency_key / op / body snapshot / **effective_requester_id** / created_at / status / applied_at
- D-Mail metadata: severity / parent_idempotency_key / op_kind / payload_digest / slack_channel_id / slack_thread_ts / **effective_requester_id** / initiating_actor_type

### Bypass candidates (= "where can this go wrong?")

1. **REST write bypass**: ADR 0030 既存 endpoint で同 token で即時 mutation。 mitigation: `RUNOPS_RPC_HIGH_MUTATION_ENABLED=1` 時 REST write を 410 Gone で disable (= 本 ADR §REST endpoint との関係)。
2. **AI agent が admin token を取得して使う (= laundering)**: server side で防げない領域だが、 token rotation 運用 + log で検知可能。 ADR 0040 §identity contract で「admin token は human-operator-bound、 AI agent path は別 endpoint」 と明示。
3. **registry 不在で HIGH mutation を許可してしまう**: registry 不在時は flag に関係なく HIGH mutation block (= fail-closed、 §identity contract carry)。
4. **`effective_requester_id` と `approver_id` の namespace 不一致**: 両方 Slack user_id 名前空間で確定 (= registry の operator_id = Slack user_id、 approval-ack の approver = Slack click user_id)。
5. **JSON-RPC notification (= id 不在) で side effect を許してしまう**: admin endpoint では notification を JSON-RPC error envelope (`CodeInvalidRequest` -32600) で reject、 HTTP 層は §HTTP transport の `200 OK always` に従い envelope 内 error として表現 (= dispatcher 到達後の reject なので transport-layer 401/415 とは別 path)。
6. **JSON-RPC batch request で複数 mutation を atomic に走らせる脱法**: batch は Phase 1 で不採用。

### Tests proving coverage

1. `TestRPCDispatcher_NotificationRejected` = id 不在 envelope を JSON-RPC error -32600 + HTTP 200 で reject (= §B-2 で実装済)
2. `TestRPCDispatcher_BatchRejected` = batch envelope を JSON-RPC error -32600 + HTTP 200 で reject (= Phase 1 不採用、 §B-2 で実装済)
3. `TestRPCAuth_TokenRegistryHit_ExtractsOperatorID` = registry hit → effective_requester_id 確定
4. `TestRPCAuth_TokenRegistryMiss_401` = registry miss → 401
5. `TestRPCAuth_RegistryFileAbsent_HighMutationBlocked` = registry 不在 + flag on でも HIGH method は -32000 block
6. `TestRPCProjectAdd_HighSeverityCreatesPending` = add で pending state 作成 + JSON-RPC result `{idempotency_key, status: "pending_approval"}`
7. `TestRPCProjectAdd_IdempotentRetry` = 同 params の retry で同 idempotency_key
8. `TestRPCProjectArchive_HighSeverityCreatesPending` = archive 同上
9. `TestRPCProjectPendingGet_PollingReturnsStatus` = polling で status 取得
10. `TestRPCProjectList_LowSeverityImmediate` = list は LOW で immediate registry passthrough
11. `TestAdminApprovalOrchestrator_RejectsSelfApproval` = `effective_requester_id == approver_id` で reject (= ADR 0035 carry)
12. `TestAdminApprovalOrchestrator_AppliesOnApprovalAck` = approval-ack pickup で Firestore registry に apply
13. `TestRESTWriteDisabled_When_FlagOn` = `RUNOPS_RPC_HIGH_MUTATION_ENABLED=1` で REST POST /admin/projects が 410 Gone
14. `TestRESTReadOnlyKept_RegardlessOfFlag` = REST GET は flag に関係なく keep

## Consequences

### Positive

- transport 非依存設計で WebSocket / WebRTC future 拡張余地確保
- JSON-RPC method 追加が name 追加だけで済む (= REST URL routing 設計不要)
- approval gate を transport-agnostic な envelope status field で表現
- 既存 §2-a/§2-b/§2-c (= domain + port + adapter) を 100% 再利用 (= transport 非依存)
- ADR 0035 4-eyes invariant が `effective_requester_id != approver_id` で同 namespace 検証成立
- REST write disable で approval gate bypass 完全閉鎖

### Negative

- 既存 REST `/admin/projects` write が deprecated path に (= operator UX 変更、 cdr-project 経由 client は JSON-RPC に migrate 必要)
- multi-token registry の operational overhead (= file deploy + Cloud Run revision bump for rotation)
- transport 抽象化が implementation cost (= 単一 HTTP のみなら直接実装の方が簡単、 future WS/WebRTC 想定が前提)
- JSON-RPC client tooling (= curl 直叩き less ergonomic、 client SDK か helper script 必要)

### Neutral

- ADR 0039 (= Proposed) を supersede、 ADR 0030 を partial supersede (= write only)
- WebSocket / WebRTC は interface 公開のみ、 具象は future PR (= YAGNI 回避)

## Out of scope

- WebSocket transport 具象実装 (= future)
- WebRTC transport (= future)
- batch request (= Phase 1 不採用)
- notification (= admin endpoint 不採用)
- 既存 REST read-only deprecate (= 別 plan)
- 軸 D (AI agent forbidden path test)
- 軸 A (full E2E integration test)

## References

- [refs/HTMLification/docs/issues/0024-multi-project-lifecycle-followup.html](../../../tap/refs/HTMLification/docs/issues/0024-multi-project-lifecycle-followup.html) — parent issue 軸 B
- [dotfiles/docs/adr/0013-project-lifecycle-severity-classification.md](../../../dotfiles/docs/adr/0013-project-lifecycle-severity-classification.md) — severity policy pin
- ADR 0019 — HIGH severity D-Mail Slack 4-eyes approval (本 ADR 再利用)
- ADR 0026 — Firestore production deploy (pending state storage)
- ADR 0030 — HTTP admin endpoint authentication (本 ADR で partial supersede、 read-only は keep)
- ADR 0035 — AI agent cannot approve AI agent (4-eyes invariant carry)
- ADR 0036 — Phase 4a approval actor-type validation (effective_requester_actor_type carry)
- ADR 0039 — admin endpoint severity-aware approval gate (本 ADR で supersede)
- JSON-RPC 2.0 specification: https://www.jsonrpc.org/specification
