# Issue 0001: workspace VM に dmail-receiver / dmail-emitter を systemd unit として deploy

**Repo:** `hironow/dotfiles` (本リポ範囲外) + 本リポの Phase 2 image build
**Status:** ✅ 完了 (2026-05-06)。 受入基準 1〜5 全 verify 済。 関連 PR: dotfiles #89 (Phase 1) + #90 (chmod 0777 fix) + #91 (boot disk 30 GB + IaC drift policy)、 runops-gateway #33 (Phase 2 image build) + #34 (release) + #35/36 (IAM apply trigger) + #37 (cdr workflow + ADR 0023 trade-off docs) + #38 (release)
**Blocker for:** Issue 0003 (Phase 3 outbound 実運用化), Issue 0002 (5本柱 frontmatter trace 連結)

> **配置先の確定 (2026-05-06)**: 当初タイトルは「exe-coder VM」 だったが、 [`experiments/2026-05-06_dotfiles-dmail-daemon-placement.md`](../../experiments/2026-05-06_dotfiles-dmail-daemon-placement.md) の調査で **配置先 = 各 workspace VM の host OS systemd** に確定。 exe-coder VM (control plane) には 5本柱 archive が存在しないため不適切。 5本柱は各 workspace VM 内で動作するので、 dmail daemon も同 VM に同居させる (per-VM = singleton、 race ゼロ、 Pub/Sub load-balancing で受信は自動 multiplex)。

## Why

本リポ (runops-gateway) は `cmd/dmail-receiver` と `cmd/dmail-emitter` の binary を提供するが、 **実 deploy は dotfiles 管轄**。 production の Pub/Sub `dmail-inbound-receiver` subscription には backlog が積み上がる一方で消費者なし (`docs/handover.md` ハマりどころ集 8-prepre 参照)。 このまま 14 日経つと message が retention で消失する。

## What

調査結果 (Coder OSS / Pub/Sub 多重 puller / container daemon supervision) を踏まえた最終構造:

- 配置: **各 workspace VM の host OS systemd** + `docker run --rm` (再起動は systemd `Restart=on-failure` で担保。 `--rm` と `--restart` は docker 上で mutually exclusive なので同時指定はしない)
- supervisor 不要 (supervisord / s6-overlay / systemd-user は採用しない)
- `/var/lib/phonewave/{archive,outbox}` を host OS dir として用意し、 devcontainer (5本柱) と dmail container 両方に bind mount
- image は本リポで build & AR publish、 dotfiles 側は image tag を `cdr templates push --variable` で pin
- (詳細は [`experiments/2026-05-06_dotfiles-dmail-daemon-placement.md`](../../experiments/2026-05-06_dotfiles-dmail-daemon-placement.md))

## Phase 1 — workspace VM template + tests (2026-05-06 GREEN)

dotfiles 側 (branch `hironow/issues-from-runops-gateway`):

| Commit | What |
|---|---|
| `941deb1` | test(exe): RED — pin dmail-receiver/emitter placement on workspace VM |
| `66edeb6` | test(exe): refactor — `dmail_unit_bodies` fixture + line-continuation regex |
| `90361a1` | feat(exe): GREEN — emit /etc/systemd/system/dmail-{receiver,emitter}.service via main.tf startup_script + bind-mount /var/lib/phonewave |
| `50e0f80` | test(exe): enable systemd-analyze verify on dmail units (12/12 passing) |

`exe/coder/templates/dotfiles-devcontainer/main.tf` に追加された要素:

- `dmail_receiver_image` / `dmail_emitter_image` / `dmail_sa_email` template 変数 (default は `:placeholder` tag)
- workspace VM startup_script で:
  - `/var/lib/phonewave/{archive,outbox}` を `install -d` で作成
  - devcontainer の `docker run` に `--volume /var/lib/phonewave:/var/lib/phonewave` を追加 (5本柱が同 dir を見える形)
  - `/etc/systemd/system/dmail-{receiver,emitter}.service` を heredoc で書き出し (Restart=on-failure、 ExecStart=/usr/bin/docker run --rm --name <unit> --network host -v <volume> <env list> ${var.image})
  - `systemctl daemon-reload && systemctl enable --now dmail-{receiver,emitter}.service`

`tests/exe/test_dmail_daemon_placement.py` (Python pytest) に追加された 12 件 (10 静的 + 2 systemd-analyze):

- 静的: image 変数宣言、 unit file 出力、 Restart=on-failure、 ExecStart=docker run + image 変数参照、 phonewave volume mount (receiver / emitter / devcontainer 各 1)
- systemd-analyze verify: dmail-receiver / dmail-emitter (parametrize)

## Phase 2 — image build & AR publish (2026-05-06、 本リポ 3 commit、 push 待ち)

本リポ (branch `docs/dmail-daemon-placement-experiment`):

| Commit | What |
|---|---|
| `fe1507d` | docs(experiments): 配置設計の調査結果ドキュメント化 |
| `8aff121` | build(docker): `docker/dmail-receiver.Dockerfile` + `docker/dmail-emitter.Dockerfile` 追加 (multi-stage、 distroless-nonroot) |
| `6f1e192` | ci(cd): CD workflow に dmail-receiver / dmail-emitter の build & push step 追加 (`<region>-docker.pkg.dev/<project>/runops/{dmail-receiver,dmail-emitter}:<sha>`) |

push 後の deploy で AR に image が publish される。 dotfiles 側の `dmail_receiver_image` / `dmail_emitter_image` default は `:placeholder` のままなので、 そのままでは workspace 起動時に `docker pull` が失敗する (= startup_script の `|| echo "...will retry"` で degrade)。 image 反映後に `cdr templates push --variable` で実 tag に切り替える。

## Phase 3 — workspace SA への IAM 反映 + image tag flip + workspace 起動 (2026-05-06 実施)

実施済:

1. **GitHub Variable `EXE_CODER_VM_SA_EMAIL`** を `exe-workspace@gen-ai-hironow.iam.gserviceaccount.com` に切替 (旧値 `exe-coder@…` は ADR 0023 の workspace VM 配置と矛盾、 さらに本リポ PR #33 で追加した validation block が reject する)
2. **本リポで tofu apply** (release PR #34 + #35 + #36 経由): IAM 4 件 add (`roles/artifactregistry.reader` on runops AR repo / `roles/pubsub.subscriber` on `dmail-inbound-receiver` / `roles/pubsub.publisher` on `dmail-outbound` / `roles/cloudtrace.agent` project-level、 全部 `exe-workspace@…` に対して)。 monitoring resource (DLQ alert / notification channel) は `DLQ_ALERT_EMAIL=hironow365@gmail.com` のおかげで destroy せず維持
3. **dotfiles で `cdr templates push exe-dotfiles-devcontainer`**: `dmail_receiver_image` / `dmail_emitter_image` を `:placeholder` から real SHA tag (`asia-northeast1-docker.pkg.dev/gen-ai-hironow/runops/dmail-{receiver,emitter}:85d4c2baa...`) に flip
4. **検証用 workspace `test-ws-001`** を起動 (`cdr create ... --parameter instance_type=e2-micro`、 GCP Region prompt は `--yes` でも skip 不可で interactive で Tokyo 選択)
5. **dmail container 動作確認**: dmail-emitter / dmail-receiver の起動ログ + Pub/Sub pull (= IAM 効いている) は OK

検出された問題:

- **dmail-receiver `permission denied: open /outbox/.tmp-...`**: distroless `:nonroot` (uid 65532) が host VM の `/var/lib/phonewave/outbox` (= mode 0755 owned by linux_user uid 1000-ish) に書けない
- 本問題の trade-off + fix 採用案 = ADR 0023 の Negative consequences に追記済 (= `chmod 0777`、 workspace VM が per-user + short-lived + tag:exe-workspace 内 trust boundary なので acceptable)
- 修正 PR:
  - dotfiles `fix/dmail-outbox-permission` (PR #90) — main.tf startup-script の `install -d -m 0755` → `0777`
  - runops-gateway `docs/cdr-runbook-and-permission-fix` (PR #37) — CLAUDE.md cdr 運用 section + ADR 0023 trade-off 追記

残タスク (= 受入基準 verify):

1. **dotfiles PR #90 merge** → main で chmod 0777 反映
2. **`cdr templates push`** で template 再 push (or PR merge 後に dotfiles template が自動更新されるかは Coder server の trigger による)
3. **workspace 再起動**: `cdr stop test-ws-001 && cdr start test-ws-001` で startup-script 再実行 → `chmod 0777` 反映 (or `cdr delete && cdr create` で確実に新 VM)
4. **5本柱への dispatch 確認**: Slack で `/runops sightjack ...` を投げ、 5本柱が D-Mail を inbox で受信できることを verify
5. **`docs/runbooks/dlq.md` "First-time setup" seek** を 1 度実行 (過去 backlog 復活)

## Service Account の前提条件 (本リポで apply 済)

- `exe-coder@gen-ai-hironow.iam.gserviceaccount.com` には以下が grant 済み (本リポ `tofu/iam_pubsub.tf` + `tofu/telemetry.tf`):
  - `roles/pubsub.subscriber` on `dmail-inbound-receiver`
  - `roles/pubsub.publisher` on `dmail-outbound` topic
  - `roles/cloudtrace.agent` (project-level)
- ただし dmail daemon が動く SA は **dotfiles 側 var.workspace_sa_email** (`exe-workspace@…`)。 上記 `exe-coder` SA とは別。 Phase 3 で workspace SA に同じ Pub/Sub IAM を grant し直す必要あり (dotfiles 側 tofu か runops-gateway 側 tofu のいずれか、 Phase 3 で確定)

## 受入基準

1. `systemctl status dmail-receiver` / `dmail-emitter` が `active (running)` で表示される
2. `dmail-inbound-receiver` の backlog が消化される (`gcloud pubsub subscriptions describe dmail-inbound-receiver` で `numUndeliveredMessages: 0`)
3. phonewave outbox dir に `.md` ファイルが atomic write される (5本柱が consume できる形式)
4. Cloud Trace に `dmail-receiver` / `dmail-emitter` service の span が出る (gcp.project_id 修正済、 本リポ PR #21)
5. `docs/runbooks/dlq.md` の "First-time setup" の seek を 1 度実行して過去 backlog を取り戻す

## 関連 ADR / docs (本リポ側)

- ADR 0013 (Pub/Sub bridge)
- ADR 0015 (dmail-receiver / dmail-emitter は本リポで管理、 deploy は別リポ)
- ADR 0018 (outbound pull subscription)
- ADR 0020 / 0021 (OTel direct OTLP + Pub/Sub trace)
- [`experiments/2026-05-06_dotfiles-dmail-daemon-placement.md`](../../experiments/2026-05-06_dotfiles-dmail-daemon-placement.md) (配置設計、 OSS Coder / Pub/Sub / supervisor 比較)
- `docs/handover.md` ハマりどころ集 8-prepre (DLQ は consumer 必須)
- `docs/runbooks/dlq.md`
