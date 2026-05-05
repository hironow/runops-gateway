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
