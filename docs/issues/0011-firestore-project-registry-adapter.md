# 0011 Firestore project registry adapter (production)

**Target repo:** `hironow/runops-gateway` (本リポ)
**Priority:** P1 (Phase α production cutover blocker)
**Depends on:** 0009 (port + SQLite adapter)
**Blocks:** Phase α production deploy
**Cross-ref:** ADR 0025 (port/adapter dual strategy、本 issue で実装する production adapter)
**Status:** 📝 未着手

## 概要

#0009 で確立した `port.ProjectRegistry` interface に対し、
**Firestore native adapter** を実装。これが Cloud Run multi-instance での
production deploy を可能にする (managed persistence)。

ADR 0025 で採用した dual adapter strategy の片方:
- SQLite (`#0009` 着地済): dev / test / operator local Mac
- **Firestore (本 issue)**: production / staging Cloud Run

## 動機

#0009 着地時点で `RUNOPS_PROJECT_REGISTRY=firestore` は
`errors.New("firestore adapter not implemented yet, see issue #0011")` を返す
fail-closed stub。本 issue で実装することで production cutover が可能になる。

## 受入基準

- [ ] `cloud.google.com/go/firestore` を依存に追加
- [ ] `internal/adapter/output/state/firestore_project_registry.go` で
      `port.ProjectRegistry` を実装 (Add/List/Get/Archive)
- [ ] collection name: `projects`、 doc ID = project.id
- [ ] field shape は SQLite schema と 1:1 対応 (Project struct そのまま、
      ArchivedAt は Firestore Timestamp で nil 許容)
- [ ] Firestore emulator を使った integration test (build tag `integration` +
      gcloud sdk 必要)
- [ ] `state.NewProjectRegistryFromEnv` の `firestore` 分岐を unimplemented
      stub から実装に切替
- [ ] env で必要: `GCP_PROJECT_ID` (Firestore project) + Cloud Run SA に
      `roles/datastore.user`
- [ ] tofu modules に Firestore database (mode: native) 作成 + role 付与
- [ ] ADR 0026 起票: production deploy における Firestore 採用 + cutover
      手順 (#0009 SQLite local seed → Firestore export/import)

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
