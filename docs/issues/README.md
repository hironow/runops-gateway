# Issues

本リポと関連リポで残っている作業 (主に **本リポ範囲外** の cross-repo
依存) のチケット置き場。本リポ内で完結する作業は通常 ADR / handover で
管理する; ここは「他リポでの実装が伴うもの + 本リポでの後続処理」を
オープンチケットとして残すのが目的。

## Index

| # | Title | Repo / Owner | Status | Blocks |
|---|---|---|---|---|
| [0001](0001-dotfiles-dmail-receiver-systemd.md) | workspace VM に dmail-receiver / dmail-emitter を systemd unit + docker run として deploy | `hironow/dotfiles` + 本リポ Phase 2 | ✅ 完了 (2026-05-06、 受入基準 1〜5 全 verify、 PR #38 / dotfiles #91 chain) | (unblocks 0002, 0003) |
| [0002](0002-five-pillars-frontmatter-trace.md) | 5本柱が D-Mail frontmatter から traceparent を読み span を再開 | 5本柱 4 リポ | 📝 未着手 | — |
| [0003](0003-phase3-outbound-enable.md) | Phase 3 outbound StreamingPull を実運用化 (`CLOUD_RUN_MIN_INSTANCES=1`) | `hironow/runops-gateway` + 0001 完了 | 📝 未着手 | — |
| [0004](0004-cloud-trace-span-verification.md) | Cloud Trace UI で実 span tree を確認 + 添付 | `hironow/runops-gateway` | 📝 未着手 | — |
| [0005](0005-async-span-flush.md) | goroutine 内 span が Cloud Run idle shutdown までに flush されず lost する | `hironow/runops-gateway` | ✅ GREEN 完了 (PendingTracker + ordered shutdown + semgrep rule) | — |
| [0006](0006-dmail-receiver-multi-path.md) | dmail-receiver の multi-project path 対応 (Pub/Sub `project_id` attr で write 先 select) | `hironow/runops-gateway` (Phase α) | 📝 未着手 | refs 0010 |
| [0007](0007-dmail-emitter-project-id-attribute.md) | dmail-emitter が emit 時に `project_id` attribute を付与 | `hironow/runops-gateway` (Phase α) | 📝 未着手 | refs 0010 |
| [0008](0008-slack-runops-project-flag.md) | Slack `/agent` command に `--project=<id>` flag 追加 | `hironow/runops-gateway` (Phase α) | 🟡 着地 (parser + validate + Pub/Sub carry + ADR 0027) | Phase α |
| [0009](0009-project-registry-sot.md) | project registry の SoT を gateway DB で持つ (port + SQLite adapter + CLI) | `hironow/runops-gateway` (Phase α) | 🟡 着地 (port/SQLite/CLI、 Firestore は #0011) | 0008, 0010 |
| [0010](0010-github-app-secret-manager.md) | GitHub App + Secret Manager 統合 (installation token fetch) | `hironow/runops-gateway` (Phase β) | 📝 未着手 | refs 0008/0011/0012 |
| [0011](0011-firestore-project-registry-adapter.md) | Firestore project registry adapter (production cutover blocker) | `hironow/runops-gateway` (Phase α) | 🟡 着地 (adapter + tofu + CI gate + ADR 0026) | Phase α prod deploy |
| [0012](0012-http-admin-endpoint-project-registry.md) | HTTP admin endpoint for project registry (production CLI 経路の正規 API) | `hironow/runops-gateway` (Phase α 完了後) | 📝 未着手 | — |

> **multiplex Phase α/β 関連 (0006-0010) は cross-repo 親 issue が `tap/refs/docs/issues/` にある。**
> 本 issue tracker が **実装 SoT**、refs 側は **dispatch 表 + cross-repo 親リスト**。
> 推奨着手順 / 依存グラフは [tap/refs/docs/issues/README.md](https://github.com/hironow/tap/blob/main/refs/docs/issues/README.md) を参照。

## ファイル命名規則

- `NNNN-short-kebab-title.md` (4 桁連番)
- 番号は **連続** に増やす (再利用しない)
- close したら status を `✅ 完了 (YYYY-MM-DD, PR #N)` に書き換え
- 完了後しばらく経って参照頻度が下がったら git rm して history に押し出す

## ADR との違い

- ADR = 決定の history (immutable)
- Issue = 未着手 / 進行中の作業 (動く target)

ADR が「なぜ X を採用したか」を記録するのに対し、Issue は「誰が次に
何をするか」を記録する。Issue を完了して ADR レベルの新しい決定が
生まれたら、対応する ADR を新規起票する。
