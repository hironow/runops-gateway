# 0023. dmail daemon は OCI image + workspace-VM 別 container で deploy する

**Date:** 2026-05-06
**Status:** Accepted

## Context

ADR 0015 (2026-05-05) は dmail-receiver / dmail-emitter の **source 管理場所** を本リポに固定した。 同 ADR の "デプロイ方式" 章 (lines 103-107) では `GitHub Release のバイナリを hironow/dotfiles の exe-coder VM startup-script で curl 取得し、 systemd 配置` と書いていたが、 これは Issue 0001 着手時の前提であり、 deploy 配置先 (= 受 VM) の調査結果を反映していなかった。

2026-05-06 の調査 ([`experiments/2026-05-06_dotfiles-dmail-daemon-placement.md`](../../experiments/2026-05-06_dotfiles-dmail-daemon-placement.md)) で 3 つの不整合が浮上した:

1. **配置先**: ADR 0015 は "exe-coder VM" (control plane) を想定していたが、 5本柱 archive は **各 workspace VM 内** にしか存在しない。 emitter が watch する archive が control plane に無いので exe-coder には emitter を置けない
2. **lifecycle 親和性**: bare binary + systemd 直起動だと、 binary upgrade のたびに dotfiles 側 startup-script を rerun する必要がある。 OCI image + tag 経由なら `cdr templates push --variable dmail_*_image=...:<sha>` で declarative に切り替えられる
3. **isolation**: 5本柱 が動く devcontainer (= workspace の主環境) と dmail daemon を **同 container に同居** させると Docker の "one service per container" 原則を破る。 supervisord (PID-1 問題で daemon 死を隠蔽) や s6-overlay (devcontainer image rebuild が必要) はいずれも次善策

加えて Pub/Sub の挙動として、 同一 subscription への複数 puller attach は **load-balanced** (broadcast ではない) なので、 receiver は 複数 workspace VM が同時に存在しても自動で multiplex される。 emitter は archive を fsnotify watch するので per-VM = singleton で運用すれば watch race ゼロ。

[Coder OSS の機能調査](https://deepwiki.com/coder/coder) では Coder Tasks も `coder_script` も workspace lifecycle に縛られる + leader-election native 機能なし、 という結果。 OCI image + per-VM systemd docker run は Coder の version drift / breaking change 影響を受けない (Coder の機能を一切使わない) のも利点。

## Decision

**dmail-receiver / dmail-emitter は本リポの CI で 2 つの OCI image (`docker/dmail-receiver.Dockerfile` / `docker/dmail-emitter.Dockerfile`) として build し、 Artifact Registry の `runops` repo に publish する。 dotfiles 側 (workspace VM template) は、 各 workspace VM の host OS systemd unit から `docker run --rm` でこれらの image を起動し、 supervision (再起動) は systemd 側 `Restart=on-failure` に寄せる (`--rm` と `--restart` は [docker engine 上で mutually exclusive](https://docs.docker.com/reference/cli/docker/container/run/) なので、 同時指定はしない)。**

ADR 0015 の "ソース管理は本リポ + デプロイは dotfiles 側" の大枠は維持し、 deploy section の **配布手段** (GitHub Release バイナリ → AR の OCI image) と **配置先** (exe-coder VM → 各 workspace VM) のみを本 ADR で確定する。

### deploy 構造

```
+--------------------------------------------------------+
| workspace VM (debian-12, per-user, host OS)             |
|                                                          |
|   systemd:                                               |
|     dmail-receiver.service (NEW)                         |
|       ExecStart=/usr/bin/docker run --rm                 |
|         --name dmail-receiver --network host             |
|         -v /var/lib/phonewave/outbox:/outbox             |
|         <gar>/runops/dmail-receiver:<sha>                |
|     dmail-emitter.service (NEW)                          |
|       ExecStart=/usr/bin/docker run --rm                 |
|         --name dmail-emitter --network host              |
|         -v /var/lib/phonewave/archive:/archive:ro        |
|         <gar>/runops/dmail-emitter:<sha>                 |
|                                                          |
|   docker:                                                |
|     coder-${owner}-${ws} (existing devcontainer)         |
|       --volume /var/lib/phonewave:/var/lib/phonewave     |
|       (5pillars + archive はこの container 内で動作)     |
+--------------------------------------------------------+
```

Legend / 凡例:

- workspace VM: 作業者の Coder workspace の GCE VM
- host OS: VM の host OS (= devcontainer の外側)
- systemd: VM host OS の systemd
- devcontainer: 既存の Coder workspace container
- 5pillars: 5本柱 (paintress / amadeus / sightjack / dominator / phonewave)
- archive: 5pillars が出力する D-Mail file 置き場

### image 設計

- multi-stage build (`golang:1.26-alpine` builder → `gcr.io/distroless/static-debian12:nonroot` runtime)
- `CGO_ENABLED=0`、 static binary
- `USER nonroot:nonroot` 明示 (Semgrep CWE-269 ガード)
- ENTRYPOINT は binary そのもの (`docker run <image> --help` がそのまま動く)
- 既存の root `Dockerfile` (= runops-gateway 本体) と同じ recipe で discipline を揃える

### CI

`.github/workflows/cd.yaml` の Build & Deploy job に 2 つの `docker/build-push-action` step を追加。 各 step は cache scope を `dmail-receiver` / `dmail-emitter` に分ける (互いの cache 衝突を防ぐ)。 push tag は:

- `<region>-docker.pkg.dev/<project>/runops/dmail-receiver:{latest,${sha}}`
- `<region>-docker.pkg.dev/<project>/runops/dmail-emitter:{latest,${sha}}`

Cloud Run deploy step は **追加しない** (workspace VM 側で起動する daemon、 Cloud Run service ではない)。

### dotfiles 側との契約

`exe/coder/templates/dotfiles-devcontainer/main.tf` (dotfiles repo) で:

- `dmail_receiver_image` / `dmail_emitter_image` template 変数を operator が pin (`cdr templates push --variable ...:<sha>`)
- workspace VM startup_script で:
  - `/var/lib/phonewave/{archive,outbox}` を host dir として作成
  - devcontainer の `docker run` に `--volume /var/lib/phonewave:/var/lib/phonewave` を追加
  - `/etc/systemd/system/dmail-{receiver,emitter}.service` を heredoc で書き出し
  - `systemctl daemon-reload && systemctl enable --now dmail-{receiver,emitter}`

### IAM

`exe-coder@` SA は ADR 0015 の Pub/Sub IAM をそのまま継承するが、 daemon が動く SA は dotfiles 側の `var.workspace_sa_email` (`exe-workspace@…`)。 後者には:

- `roles/pubsub.subscriber` on `dmail-inbound-receiver`
- `roles/pubsub.publisher` on `dmail-outbound`
- `roles/cloudtrace.agent` (project-level)
- `roles/artifactregistry.reader` on `<region>-docker.pkg.dev/<project>/runops`

を grant する必要がある。 Issue 0001 Phase 3 で本リポの tofu で apply 予定。

## Consequences

### Positive

- **race condition ゼロ**: per-VM = singleton 自動成立、 emitter watch race も発生しない
- **container best practice 維持**: 1 service per container、 supervision は systemd ネイティブ。 supervisord / s6-overlay / systemd-user 不要
- **疎結合維持**: gateway は ADR 0013 通り Pub/Sub のみで通信、 dotfiles 側で systemd unit + image tag を持つだけ
- **OSS Coder 制約と無関係**: Coder Tasks / `coder_script` / MCP server を一切使わない (Coder version drift / breaking change の影響なし)
- **upgrade が declarative**: `cdr templates push --variable dmail_*_image=...:<sha>` で workspace VM template を rebuild、 既存 binary を curl で差し替える運用より stable
- **archive 共有が単純**: workspace VM の host OS dir を devcontainer + 2 dmail container に bind mount で 1 set のファイルを 3 つの container が共有

### Negative

- **lifecycle が workspace VM と紐づく**: workspace VM が止まれば dmail も止まる。 5本柱 自体が workspace 内で動くので整合的だが、 workspace を全停止する運用日には Pub/Sub backlog が積み上がる (subscription retention 内で復活)
- **AR 内 image 数の増加**: `runops-gateway` に加え `dmail-receiver` / `dmail-emitter` の 3 image を維持
- **per-workspace VM image pull 容量**: workspace VM が複数同時稼働するとそれぞれが image を pull する (大した量ではないが auto-scale 時の cold-pull cost は発生)

### Neutral

- ADR 0015 の `cmd/dmail-receiver/` / `cmd/dmail-emitter/` の repo 内配置と `internal/core/domain/dmail.go` を Single Source of Truth とする決定はそのまま継承
- ADR 0015 の "GitHub Release のバイナリを curl 取得" という配布手段は本 ADR で **置換** (新規実装側で採用しない)。 ADR 0015 の status は維持 (deploy section が override される旨は ADR 0015 の本文 lifecycle update として 2026-05-06 update note を追加することで読み手にナビゲート)

## 関連 ADR / docs

- ADR 0005: Ports and Adapters (driven adapter としての配置)
- ADR 0012: 新しい D-Mail kind は追加しない (schema 安定性)
- ADR 0013: outbox 書き込みは Pub/Sub bridge を経由する (deploy 方式の前提)
- ADR 0015: dmail-receiver / dmail-emitter は本リポで管理する (本 ADR は ADR 0015 の deploy section を update)
- ADR 0017 / 0018: Slack thread reply / dmail-outbound pull の対称設計
- ADR 0020 / 0021: OTel direct OTLP + Pub/Sub trace context (image の起動 env で OTLP endpoint 制御)
- [`experiments/2026-05-06_dotfiles-dmail-daemon-placement.md`](../../experiments/2026-05-06_dotfiles-dmail-daemon-placement.md) — 配置設計の調査結果
- [`docs/issues/0001-dotfiles-dmail-receiver-systemd.md`](../issues/0001-dotfiles-dmail-receiver-systemd.md) — Phase 1 / 2 / 3 の実装計画
