# Project conventions for runops-gateway

本ファイルは本リポ固有の運用ルールを記録する (CLAUDE.md は project
instructions としてアシスタントが自動で読み込む)。global なルールは
`/Users/nino/.claude/CLAUDE.md` 側にある。

## Git / PR merge ポリシー

ブランチ構造は `feature → develop → main` の 2 段。merge 方法は段により
**異なる**:

| ブランチ移行 | merge 方式 | 理由 |
|---|---|---|
| feature / fix / chore → `develop` | **squash merge** (`gh pr merge <N> --squash`) | 1 機能 1 commit に潰して develop の history を読みやすく保つ。各 commit message は Conventional Commits + ADR 参照 lint を通す |
| **`develop` → `main` (release / promote)** | **merge commit** (`gh pr merge <N> --merge`) | 「いつ何を main に promote したか」を 1 つの merge commit として残し、各 release が含む feature commit を main 側からも辿れるようにする。main の linear history を **squash で潰さない** ことでロールバック対象 (前 revision) の特定が容易 |

具体例:

```bash
# Feature merge to develop
gh pr merge 7 --squash --delete-branch=false

# Release merge to main (e.g. Phase 4b, 2026-05-05)
gh pr merge 12 --merge --delete-branch=false
```

main への promote PR を間違えて squash で merge すると、各 feature の
個別 commit が main 側から消えるので **必ず** `--merge` を指定すること。

## Release promote の運用

1. develop の HEAD で全機能が動く状態を確認
2. `gh pr create --base main --head develop` で release PR を起票
3. release PR 本文に「含まれる PR 番号と要約」「本番影響」「rollback 手順」を明記
4. CI green を確認 (`gh run list --branch develop`)
5. **`gh pr merge <N> --merge`** で main へ取り込み (CD 発火)
6. CD pipeline の post-deploy smoke (`/_healthz` + `/slack/{interactive,command}` の HMAC 401 regression check) が GREEN であることを確認

post-deploy smoke の詳細と rollback 手順は `docs/handover.md` の
「ハマりどころ集 9-pre. main promote 時の smoke と rollback」を参照。

## GitHub Actions の pin ポリシー

repo settings の **"Require actions to be pinned to a full-length commit SHA"**
が ON。すべての `uses:` 行を SHA で pin する。tag (`@v3` 等) や branch
(`@main` 等) は **不可**。書式は:

```yaml
- uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
```

末尾の `# v6.0.2` コメントは可読性用 (Dependabot が更新時に併せて書き換える)。

### Allow list (org settings の "Allow or block specified actions")

非 Marketplace-verified-creator の action は明示許可が必要。本リポでは:

| action | 出所 | 許可方法 |
|---|---|---|
| `actions/*` | GitHub | "Allow actions created by GitHub" でカバー |
| `docker/*` | Docker | Marketplace verified |
| `google-github-actions/*` | Google | Marketplace verified |
| `opentofu/setup-opentofu` | OpenTofu | allow list に `opentofu/setup-opentofu@*` を追加 |
| `jdx/mise-action` | jdx (個人) | allow list に `jdx/mise-action@*` を追加 |
| `dorny/paths-filter` | dorny (個人) | **allow list には追加せず、`git diff --name-only` の self-impl で代替** (CD `check-changes` job 参照) |

新しい action を導入したいときは:

1. Marketplace で verified creator かを確認
2. verified なら自由に SHA pin で追加
3. 非 verified なら repo settings の allow list に `org/repo@*` を追加 (operator が手動で GitHub UI 操作) → SHA pin で workflow に書く
4. 自前 shell で代替できるならそれを優先 (allow list の運用負荷を最小化)

### Dependabot

`.github/dependabot.yaml` で `github-actions` と `gomod` ecosystem を週 1
チェック。SHA pin 化された action も Dependabot は新 release の SHA に
自動で書き換える。group 設定で関連 PR をまとめている (docker-actions /
google-actions / github-actions-misc / otel / gcp / go-deps-rest)。

PR が来たら CI green を確認して squash merge → develop の通常運用に乗せる。

## Go の lint policy

`go vet` に加えて **golangci-lint** で lint。Go 1.24+ の tool dependency
機能 (`go.mod tool` directive) を使い、ツール自体は `tools/go.mod` で
分離管理する (本リポの go.mod を汚さない)。

実行:

```bash
just lint   # go vet ./... + CGO_ENABLED=0 go tool -modfile=tools/go.mod golangci-lint run ./...
```

config: `.golangci.yaml` (v2 schema)。現状の有効 linter:

- standard set (govet / staticcheck / ineffassign / unused)
- revive: idiomatic Go style (var-naming は `ID/URL/HTTP/API/JSON/TLS`
  をイニシャリズム扱い)
- misspell: English spelling
- unconvert: 不要な型変換
- bodyclose: `http.Response.Body` の close 漏れ

intentionally disabled: **errcheck**。導入時点で既存コードに ~50 件の
unchecked error site があり、段階的に修正する想定。再有効化のときは
`.golangci.yaml` の `linters.disable` から `errcheck` を消し、PR で
unchecked 箇所を `_ = ...` か `if err != nil { ... }` に直す。

新しい linter を有効化する流儀:

1. PR で `.golangci.yaml` の `linters.enable` に追加
2. ローカルで `just lint` を走らせ、issues が増えるなら同 PR で fix
3. `tools/go.mod` の golangci-lint version を上げたいときは
   `go get -tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint@vX.Y.Z`

## pre-commit (prek)

[j178/prek](https://github.com/j178/prek) (Rust 製の pre-commit
互換ランナー) を採用。本家 pre-commit より起動が速く、Rust-native の
builtin hook が使える。設定: `.pre-commit-config.yaml`。

### Install (1 度だけ)

```bash
# どれか 1 つ
brew install prek                      # macOS
cargo install prek                     # Rust toolchain あり
mise use -g prek                       # mise 経由

just install-hooks                     # git hook を ~/.git/hooks/ に書き込む
```

### 日常運用

```bash
just pre-commit    # prek run --all-files (commit 前 / push 前 manual)
just check-all     # prek run + just check + just test (CI 同等の gate)
```

### 設定の方針

- **builtin hooks** (trailing-whitespace / end-of-file-fixer / check-yaml /
  check-toml / check-json / check-added-large-files / check-merge-conflict /
  detect-private-key)
- **local hooks** は `just fmt` / `just lint` / `tofu fmt -recursive tofu`
  を delegate するので、CLI と prek で同じ check が走る (二重定義を避ける)
- `go test` は重いので pre-commit には入れず `check-all` 段の `just test`
  でカバー
- 除外 path: `output/` / `node_modules/` / `.venv/` / `tofu/.terraform/`

## cdr (Coder CLI wrapper) — Issue 0001 deploy verify 手順

dmail-receiver / dmail-emitter の OCI image は本リポの CD で
Artifact Registry に publish される (ADR 0023)。 実 deploy は
`hironow/dotfiles` 側の Coder workspace template (workspace VM の
host OS systemd + docker run) で行う。 verify 時に本リポから
production の workspace 状況を確認するための `cdr` コマンド一覧
を以下に集約する。 dotfiles repo の `exe/scripts/cdr*` 系 wrapper
が実体。

### 前提

- `cdr` コマンド (`~/.local/bin/cdr`) が PATH にあること
  (= dotfiles で `just exe-cdr-install` 実行済)
- gcloud auth + dotfiles tofu state 復号化 passphrase が手元にあること
- exe-coder VM (= Coder server host) が SPOT preempt で TERMINATED
  していないこと (= preempted なら `gcloud compute instances start
  exe-coder --project gen-ai-hironow --zone asia-northeast1-a`
  1 コマンドで復旧、 startup-script で coder.service +
  cloudflared-exe.service が再立ち上がる)

### dmail Phase 3 の deploy / verify ワークフロー

```bash
# 1. workspace template に新 image tag を flip (CD で AR に publish 済の SHA)
cd ~/dotfiles
SHA=<runops-gateway main の最新 SHA>
PROJ=gen-ai-hironow
cdr templates push exe-dotfiles-devcontainer \
  -d exe/coder/templates/dotfiles-devcontainer \
  --variable project_id="$PROJ" \
  --variable workspace_sa_email="$(just exe-output -raw exe_workspace_sa_email)" \
  --variable coder_internal_url="$(just exe-output -raw coder_internal_url)" \
  --variable image="$(just exe-output -raw artifact_registry_repo)/devcontainer:main" \
  --variable dmail_receiver_image="asia-northeast1-docker.pkg.dev/$PROJ/runops/dmail-receiver:$SHA" \
  --variable dmail_emitter_image="asia-northeast1-docker.pkg.dev/$PROJ/runops/dmail-emitter:$SHA" \
  --yes

# 2. 検証用 workspace を起動 (instance_type は dev verify なら e2-micro 最小で十分。
#    GCP region prompt は --yes でも skip できないので、 interactive で Tokyo を選ぶ)
cdr create test-ws-001 \
  --template exe-dotfiles-devcontainer \
  --parameter instance_type=e2-micro \
  --parameter dotfiles_uri=https://github.com/hironow/dotfiles.git \
  --yes
# → "GCP Region" prompt → "Tokyo, Japan: asia-northeast1-a" 選択

# 3. workspace 起動状況確認 (Pending → Starting → Running、 agent connecting → ⦿ connected)
cdr show test-ws-001
# 注意: agent の connected シンボルは `⦿` (filled circle)、 `✔` ではない
# (poll で grep するなら `⦿ connected` で match)

# 4. Workspace VM の serial port log で startup-script の進捗を確認
#    (startup-script は: tailscale install → docker pull devcontainer →
#     docker pull dmail-receiver → docker pull dmail-emitter →
#     /etc/systemd/system/dmail-{receiver,emitter}.service 書き出し →
#     systemctl daemon-reload + enable --now)
VM=coder-hironow-test-ws-001-root
gcloud compute instances get-serial-port-output "$VM" \
  --project gen-ai-hironow --zone asia-northeast1-a 2>&1 | tail -40
# dmail-receiver / dmail-emitter container の log もここに出る
# (docker[<pid>] tag で grep できる)

# 5. workspace 削除 (検証完了後の cost 抑制)
cdr delete test-ws-001 --yes
```

### よくハマる罠

- **`coder ssh test-ws-001` は devcontainer の中に入る** (= host OS の
  systemctl / docker は見えない)。 dmail container と systemd unit を
  確認したいときは serial port log を使う。 `gcloud compute ssh
  <VM>` は workspace VM の OS Login が設定されていないと
  `Permission denied (publickey)` で失敗 — 確実なのは serial port log
- **`--yes` flag でも GCP Region prompt は skip 不可**。 `coder/gcp-region`
  module の制約。 interactive で Tokyo を選ぶ運用
- **`cdr` の API call が 1033 (Cloudflare Tunnel error)** で失敗するときは
  exe-coder VM が SPOT preempt で TERMINATED していないか確認。
  `gcloud compute instances list --filter="name~exe-coder"` で status を
  見て、 TERMINATED なら `gcloud compute instances start exe-coder ...`
  で復旧
- **dmail container は `USER nonroot:nonroot` (uid 65532、 distroless image
  guardrail)** で起動。 host VM の `/var/lib/phonewave/{archive,outbox}`
  の owner が違う (= devcontainer の linux_user uid 1000 系) と
  `permission denied: open /outbox/.tmp-...` で write fail する。
  dotfiles 側 main.tf startup-script の `chmod` / `chown` を整合させる
  必要 (Issue 0001 Phase 3 deploy 時の確認ポイント)
- **client (cdr) と server (Coder) の version mismatch** warning が頻出
  するが、 通常 1 minor 版差 (例 client v2.32 / server v2.31) なら
  運用上問題なし。 `cdr show` / `cdr templates push` 等は正常動作する
