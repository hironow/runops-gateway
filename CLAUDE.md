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
6. CD pipeline の post-deploy smoke (`/healthz` + `/slack/{interactive,command}` の HMAC 401 regression check) が GREEN であることを確認

post-deploy smoke の詳細と rollback 手順は `docs/handover.md` の
「ハマりどころ集 9-pre. main promote 時の smoke と rollback」を参照。
