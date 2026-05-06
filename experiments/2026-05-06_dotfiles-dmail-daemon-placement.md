# Experiment: dotfiles 側の dmail-receiver / dmail-emitter daemon 配置設計

**Date:** 2026-05-06
**Objective:** Issue 0001 (本リポ `docs/issues/0001-dotfiles-dmail-receiver-systemd.md`) の **配置先と起動方式の確定**。 Coder workspace の構造、 Pub/Sub の多重 puller 動作、 OSS 限定の Coder 機能、 一般的な container daemon supervision pattern を踏まえて、 race ゼロ + 疎結合 + container best practice を満たす配置を選ぶ。
**Status:** 🟢 Complete (設計確定、 dotfiles 側で着手予定)

## Background

production で `/slack/interactive` Approve 後の dispatch trace は届くようになったが (Issue 0005 GREEN, PR #31)、 5本柱 (paintress / amadeus / sightjack / dominator / phonewave) からの outbound D-Mail は **dmail-emitter が稼働していない** ため Slack thread reply に流れない。 また `dmail-inbound` topic に publish された message は **dmail-receiver が稼働していない** ため 14 日 retention で消失する (`docs/handover.md` ハマりどころ集 8-prepre)。

両 binary は ADR 0015 で本リポ (`cmd/dmail-receiver` / `cmd/dmail-emitter`) で source 管理、 deploy は dotfiles 管轄と決まっている。 Issue 0001 が「systemd unit として deploy」 と起票されているが、 **どの host で動かすか** + **どう supervision するか** の確定が必要。

## Hypothesis

1. **workspace VM の host OS で systemd unit + docker run** が最良案
2. workspace **container 内** で daemon は走らせない (container best practice 違反)
3. supervisord / s6-overlay は **次善策**、 上記が選べる限り不要

## Experiment Design

### 調査軸 (3 つ)

1. **Coder OSS 機能調査**: 2.29 (2026 GA) の Tasks / `coder_script` / MCP server / Agent lifecycle、 OSS で使える範囲、 daemon supervision を native に提供するか
2. **Pub/Sub 多重 puller race**: 同一 subscription に複数 subscriber が attach した場合の挙動 (broadcast / load-balanced)、 exactly-once delivery の制約
3. **runops-gateway 側責務マッピング**: ADR 0012-0021、 Pub/Sub topology、 idem 発行・検証境界、 dotfiles 側に降ろす契約

### Sources (公式優先)

- [Coder 2.29 changelog](https://coder.com/changelog/coder-2-29)
- [Coder MCP Server docs](https://coder.com/docs/ai-coder/mcp-server)
- [Coder pricing (OSS / Premium)](https://coder.com/pricing)
- [DeepWiki coder/coder](https://deepwiki.com/coder/coder)
- [Pub/Sub subscriber pull docs](https://docs.cloud.google.com/pubsub/docs/subscriber)
- [Pub/Sub exactly-once delivery](https://docs.cloud.google.com/pubsub/docs/exactly-once-delivery)
- [Docker multi-service container guidance](https://docs.docker.com/engine/containers/multi-service_container/)
- [s6-overlay GitHub](https://github.com/just-containers/s6-overlay)
- [supervisord vs s6-overlay (serversideup)](https://serversideup.net/open-source/docker-php/docs/guide/using-s6-overlay)

## Results

### 1. Coder OSS 範囲 (2026-05)

| 機能 | OSS | Premium | 用途 |
|---|---|---|---|
| Coder Tasks (`coder_ai_task`) | ✅ (1,000 build 上限) | ✅ unlimited | event-driven background job |
| `coder_script` (run_on_start / run_on_stop / cron) | ✅ | ✅ | workspace lifecycle hook |
| `coder ssh <ws> <cmd>` | ✅ | ✅ | 外部から workspace に command 注入 |
| Local MCP server (`coder exp mcp server`) | ✅ | ✅ | external AI agent → Coder |
| HTTP MCP server (`mcp-server-http` experiment) | ✅ experimental | ✅ | 同上 (HTTP) |
| WorkspaceBash tool (Background flag) | ✅ via MCP | ✅ | command 実行 (timeout 後 background 化可) |
| AI Bridge | ❌ | ✅ Beta | MCP tool auto-injection |
| Workspace Proxy / Prebuilt Workspaces / Audit logs | ❌ | ✅ | — |

**重要事実**:
- Coder Tasks も `coder_script` も **workspace lifecycle に縛られる** (workspace stop で daemon 死)
- **ネイティブ leader-election は無し**
- `coder_script` は **同期実行**、 child process は **workspace shutdown で process group ごと kill** される (DeepWiki: `agent/agentscripts/agentscripts.go:251-308`)
- `run_on_stop` は **timeout 内完了保証なし**、 timeout 後 SIGINT → 数秒後 SIGKILL
- `run_on_start` は **非冪等** (agent 再起動ごとに毎回再実行)
- **Coder 公式 example で daemon supervision の prescribed pattern は無い**、 supervisord は passing mention のみ

### 2. Pub/Sub 多重 puller 動作

- **複数 subscriber 同時 attach は load-balanced** (各 subscriber が subset 受信、 broadcast ではない)
- exactly-once delivery: **Pull / StreamingPull のみ**、 同一 region 内、 ack deadline default 60s
- 順序: ordering keys を使えば 同 key の message は順序保証 (本リポ inbound は `target_tool` を ordering key)

→ **receiver の多重化は「正常動作」**。 race ではなく load-balancing。
→ **emitter (publisher) の多重化は archive watch 重複が問題**。 同 archive を複数 emitter が watch すると同 message を多重 publish (dedup は subscriber 側責任)。

### 3. runops-gateway 側責務マッピング (要点)

- **ADR 0013 (Accepted)**: gateway ↔ exe-coder VM の唯一の通信路 = Pub/Sub のみ。 SSH / ファイル直書き禁止
- **ADR 0015 (Accepted)**: binary は本リポで build、 deploy は dotfiles 側
- **ADR 0021 (Accepted)**: Pub/Sub trace context は `cloud.google.com/go/pubsub/v2 v2.5.1+` の `EnableOpenTelemetryTracing: true` に完全委譲、 attribute `traceparent` を手書きしない
- **dotfiles 側に降ろす契約 (固定済)**:
  - inbound subscription: `dmail-inbound-receiver` (ack 60s, ordering ON, DLQ→`dmail-inbound-dlq` after 5 attempts)
  - outbound topic: `dmail-outbound`
  - SA: `exe-coder@gen-ai-hironow.iam.gserviceaccount.com`
  - filename rule: `<id>.md` (D-Mail.ID = 16 byte crypto/rand hex)
  - atomic write: temp + rename
  - schema v1 frontmatter
  - OTLP gRPC + `EnableOpenTelemetryTracing: true`
- **cross-process dedup の最終責任**: dotfiles 側 receiver の `(idempotency_key, filename existence)` 検査 (`internal/adapter/output/phonewave/writer.go:48-52`)

### 4. dotfiles 構造 (2026-05-06 時点)

- **exe-coder VM** (control plane): ubuntu-24.04、 SPOT、 e2-small、 既に `coder.service` / `cloudflared-exe.service` / `cloud-sql-proxy.service` / `tailscaled` が systemd で常駐
- **workspace VM(s)**: per-workspace で provisioned (`coder-${owner}-${ws_name}-root`)、 debian-12、 e2-small、 `tag:exe-workspace`、 startup-script で tailscale + docker + `docker run --restart=unless-stopped` で devcontainer image を起動
- **template**:
  - `dotfiles-devcontainer` (interactive): hironow が手動で起動
  - `dotfiles-job` (ephemeral, ADR 0008): cdr-job 経由で短命 task
- **5本柱の動作場所**: **workspace VM 内** (作業者の git 管理環境、 archive も同 VM 内)

### 5. supervisor 候補比較 (もし container 内に置く場合)

| 候補 | container 親和性 | health 検出 | race | 学習コスト | Coder 親和性 |
|---|---|---|---|---|---|
| supervisord | △ PID 1 不適切 (supervisord 健康 = container 健康と誤認、 daemon 死隠蔽) | △ | OK | ◎ | △ passing mention |
| s6-overlay | ◎ container PID 1 設計、 ゾンビ reaping 完備、 正確な exit | ◎ | OK | △ | △ |
| systemd-user (linger) | △ cgroup v2 / dbus 必要 | △ | OK | △ | △ |
| `coder_script run_on_start` 直 spawn | ◎ Coder native | × supervision 無し | OK | ◎ | ◎ |

→ **container 内に置くなら s6-overlay > supervisord > systemd-user**。 ただし **真の最良解は container の外**。

## 設計判断

### 採用: **workspace VM の host OS systemd unit + docker run** (= 案 A')

```
+--------------------------------------------------------+
| workspace VM (debian-12, per-workspace, host OS)        |
|                                                          |
|   systemd:                                               |
|     coder-agent-vm.service          (existing)           |
|     dmail-receiver.service          (NEW)                |
|       ExecStart=docker run --restart=unless-stopped \   |
|         --name dmail-receiver \                          |
|         -v /var/lib/phonewave/outbox:/outbox \           |
|         <gar>/runops-gateway-dmail-receiver:<sha>       |
|     dmail-emitter.service           (NEW)                |
|       ExecStart=docker run --restart=unless-stopped \   |
|         --name dmail-emitter \                           |
|         -v /var/lib/phonewave/archive:/archive \         |
|         <gar>/runops-gateway-dmail-emitter:<sha>        |
|                                                          |
|   docker:                                                |
|     coder-${owner}-${ws}    (existing devcontainer)     |
|       --volume /var/lib/phonewave:/root/tap/phonewave   |
|       (5pillars + archive はこの container 内で動作)     |
+--------------------------------------------------------+
```

Legend / 凡例:

- workspace VM: 作業者の Coder workspace VM
- host OS: VM のホスト OS (= devcontainer の外側)
- systemd: VM の host OS systemd
- devcontainer: 既存の Coder workspace container
- 5pillars: 5本柱 (paintress / amadeus / sightjack / dominator / phonewave)
- archive: 5pillars が出力する D-Mail file 置き場

### なぜこれが最良か

1. **race ゼロ**: per-VM = singleton 自動成立。 emitter の archive watch 重複も発生しない (1 VM = 1 archive set)
2. **container best practice 維持**: 1 service per container を守る (devcontainer = 5本柱 + 開発環境、 dmail container = receiver / emitter 各 1 個)
3. **supervision = systemd ネイティブ**: Restart=on-failure、 dotfiles の既存 systemd unit (`coder.service` 等) と同じ流儀。 supervisord / s6-overlay 不要
4. **疎結合維持**: gateway repo は ADR 0013 通り Pub/Sub のみで通信。 dotfiles 側で systemd unit + image tag を持つだけ
5. **OSS Coder 制約と無関係**: Coder Tasks / coder_script を一切使わない (Coder version drift / breaking change の影響なし)
6. **archive 共有が単純**: workspace VM の host OS dir を **devcontainer に volume mount** + **dmail container に volume mount** すれば、 5本柱 (devcontainer 内) と dmail-receiver/emitter (host OS docker) が同じ archive / outbox を共有
7. **trace 連結**: ADR 0021 通り `EnableOpenTelemetryTracing: true` で繋がる
8. **lifecycle**: workspace VM start で daemon up、 stop で down。 5本柱が動かない時期は dmail も止まる (整合性 OK)、 backlog は subscription retention 内で復活

### 既存 dotfiles 流儀との整合

- `exe/coder/templates/dotfiles-devcontainer/main.tf` の startup_script は既に `docker run --restart=unless-stopped` で devcontainer を起動する流儀 (line 308-319)
- 同じ startup_script に dmail-receiver / dmail-emitter container の `docker run` を 2 つ追加するだけ
- systemd unit にするか直 docker run にするかは選択肢:
  - **systemd unit** (推奨): journalctl で log 集約、 restart policy 統一
  - 直 docker run: 軽量、 ただし systemd unit の方が dotfiles 既存流儀と整合

### 不採用案と理由

- **案 B (workspace container 内 daemon)**: container best practice 違反、 PID 1 問題、 supervision 自前
- **案 C (別 Cloud Run service)**: archive watch ができない (5本柱 archive は workspace VM 内)
- **案 D (Coder API 経由 control plane → workspace command 注入)**: 自前 leader-election 必須、 lifecycle 保証薄い、 OSS で Background flag は experimental
- **案 E (exe-coder VM の host OS systemd)**: archive が見えない (= NFS 共有の運用コスト)、 control plane と daemon を同居させる責務混在

## Conclusion

Issue 0001 の **配置先 = workspace VM の host OS systemd**、 **起動方式 = `docker run --restart=unless-stopped`** で確定。 supervisor 系 (supervisord / s6-overlay / systemd-user) は使わない。

### 次のアクション (dotfiles 側、 本リポ範囲外)

1. **binary distribution**: `cmd/dmail-receiver` / `cmd/dmail-emitter` を Docker image として Artifact Registry に publish (本リポの CI に追加)
2. **dotfiles 側 systemd unit**:
   - `exe/coder/templates/dotfiles-devcontainer/main.tf` の startup_script に dmail-receiver.service / dmail-emitter.service を追加
   - `docker run --restart=unless-stopped` で daemon container 起動
   - archive / outbox を host OS dir 経由で devcontainer と共有 (`-v /var/lib/phonewave/...`)
3. **5本柱 archive path の整理**: devcontainer 側 mount path を `/var/lib/phonewave/archive` (host) → `/root/tap/...` (container) に整える
4. **テスト**: testcontainers ベースの E2E テストで Pub/Sub emulator + dmail-receiver 起動 + outbox write 確認

### Issue 0001 への反映

dotfiles 側で着手するときの env 例 (Issue 0001 原案を更新):

```ini
[Service]
Environment=PUBSUB_PROJECT_ID=gen-ai-hironow
Environment=PUBSUB_DMAIL_INBOUND_SUB=dmail-inbound-receiver
Environment=PHONEWAVE_OUTBOX_DIR=/outbox
Environment=GOOGLE_CLOUD_PROJECT=gen-ai-hironow
Environment=OTEL_EXPORTER_OTLP_ENDPOINT=telemetry.googleapis.com:443
Environment=OTEL_EXPORTER_OTLP_PROTOCOL=grpc
Environment=OTEL_SERVICE_NAME=dmail-receiver
Environment=OTEL_TRACES_SAMPLER=parentbased_traceidratio
Environment=OTEL_TRACES_SAMPLER_ARG=1.0
ExecStart=/usr/bin/docker run --rm --name dmail-receiver \
  -v /var/lib/phonewave/outbox:/outbox \
  -e PUBSUB_PROJECT_ID -e PUBSUB_DMAIL_INBOUND_SUB \
  -e PHONEWAVE_OUTBOX_DIR -e GOOGLE_CLOUD_PROJECT \
  -e OTEL_EXPORTER_OTLP_ENDPOINT -e OTEL_EXPORTER_OTLP_PROTOCOL \
  -e OTEL_SERVICE_NAME -e OTEL_TRACES_SAMPLER -e OTEL_TRACES_SAMPLER_ARG \
  asia-northeast1-docker.pkg.dev/gen-ai-hironow/runops/dmail-receiver:latest
ExecStop=/usr/bin/docker stop -t 10 dmail-receiver
Restart=on-failure
RestartSec=10s
```

dmail-emitter も同パターン (PHONEWAVE_ARCHIVE_DIRS は archive root を指す)。

## 関連

- ADR 0012 / 0013 / 0015 / 0017 / 0018 / 0020 / 0021
- `docs/handover.md` ハマりどころ集 8-prepre (DLQ は consumer 必須)
- `docs/issues/0001-dotfiles-dmail-receiver-systemd.md`
- `docs/issues/0003-phase3-outbound-enable.md` (Phase 3 outbound 実運用化)
- `experiments/2026-05-05_otel-cloud-run-pubsub-jaeger.md`
- dotfiles repo: `exe/coder/templates/dotfiles-devcontainer/main.tf`、 `exe/docs/architecture.md`、 ADR 0008 / 0009
