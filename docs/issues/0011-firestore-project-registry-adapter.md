# 0011 Firestore project registry adapter (production)

**Target repo:** `hironow/runops-gateway` (本リポ)
**Priority:** P1 (Phase α production cutover blocker)
**Depends on:** 0009 (port + SQLite adapter)
**Blocks:** Phase α production deploy
**Cross-ref:** ADR 0025 (port/adapter dual strategy、本 issue で実装する production adapter)、 ADR 0026 (本 PR で起票、 named DB / cleanup / CI gate)
**Status:** 🟡 着地 (port + Firestore adapter + tofu named DB + CI gate + ADR 0026、 production cutover は operator が手順実施)

## 概要

# 0009 で確立した `port.ProjectRegistry` interface に対し、
**Firestore native adapter** を実装。これが Cloud Run multi-instance での
production deploy を可能にする (managed persistence)。

ADR 0025 で採用した dual adapter strategy の片方:

- SQLite (`#0009` 着地済): dev / test / operator local Mac
- **Firestore (本 issue)**: production / staging Cloud Run

## 動機

# 0009 着地時点で `RUNOPS_PROJECT_REGISTRY=firestore` は
`errors.New("firestore adapter not implemented yet, see issue #0011")` を返す
fail-closed stub。本 issue で実装することで production cutover が可能になる。

## 受入基準

- [x] `cloud.google.com/go/firestore` を依存に追加 (v1.22)
- [x] `internal/adapter/output/state/firestore_project_registry.go` で
      `port.ProjectRegistry` を実装 (Add/List/Get/Archive)
- [x] collection name: `projects` (DefaultProjectsCollection、 RUNOPS_FIRESTORE_COLLECTION で override 可)
- [x] field shape は SQLite schema と 1:1 対応 (Project struct + firestore tag)
- [x] Firestore emulator を使った integration test (build tag `integration`、
      collection per-test 隔離、 8 ケース)
- [x] `state.NewProjectRegistryFromEnv` の `firestore` 分岐を unimplemented
      stub から実装に切替 (env-driven default DB / named DB)
- [x] env: `GOOGLE_CLOUD_PROJECT` 必須 (既存 convention と整合、 GCP_PROJECT_ID は採用せず) + Cloud Run SA に `roles/datastore.user`
- [x] tofu modules に Firestore database (named: `runops-registry`、 mode: FIRESTORE_NATIVE) + IAM
- [x] ADR 0026 起票: production deploy における Firestore 採用 + cutover 手順
- [x] CI に firestore-integration job 追加 (emulator + integration test、 production cutover gate)
- [x] factory signature 拡張 (CleanupFunc 追加、 Cloud Run 長時間稼働で client.Close 保証)

### defer 項目 (本 issue scope 外)

- [ ] **#0012** HTTP admin endpoint (production の registry 操作経路、 CLI dev only から分離)
- [ ] cutover seed tool 自動化 (operator が SQLite → Firestore に手動 add する形で代替、 ADR 0026 に手順記載) — 将来 #0013 lifecycle CLI が pick up (#0009 SQLite local seed → Firestore export/import)

## 実装ヒント

- Firestore client: `firestore.NewClient(ctx, projectID)` でシングルトン
- Add: `Doc(id).Create(ctx, p)` で UNIQUE 制約は `codes.AlreadyExists` で判定 → ErrProjectAlreadyExists
- Archive: `Doc(id).Update(ctx, []firestore.Update{{Path: "Status", Value: archived},{Path: "ArchivedAt", Value: time.Now().UTC()}})`、
  not found は `codes.NotFound` で判定 → ErrProjectNotFound
- List filter: `collection.Where("status", "==", string(filter.Status)).Documents(ctx)`
- `golang.org/x/sync/singleflight` で同 project 並列要求の dedup (cache 層は将来追加)

## 関連

- 0009 (port + SQLite、本 issue の interface 共有)
- 0012 (HTTP admin endpoint、 production registry 操作経路)
- ADR 0025 (本 issue 着地後、 ADR 0026 と pair)
