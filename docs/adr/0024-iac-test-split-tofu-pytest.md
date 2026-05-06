# 0024. IaC test は 役割で分割する: tofu test (動的) / pytest (静的 + 外部 binary)

**Date:** 2026-05-06
**Status:** Accepted

## Context

Issue 0001 Phase 1 + codex pre-push review session で `exe/coder/templates/dotfiles-devcontainer/main.tf` (workspace VM template) と `tofu/variables.tf` (本リポ) に dmail 関連の variable + validation block + heredoc 内 systemd unit が増えた。 当初 test は **すべて pytest + Python regex** で書いていた:

- variable declaration の存在 (regex で `variable\s+"..."`)
- variable default 値 (HCL_SUBSTITUTIONS 自前テーブルで dummy 置換)
- variable validation block の有無 (regex で block の存在のみ)
- heredoc 内 systemd unit body の string contains
- `systemd-analyze verify` (Docker container 内で外部 binary)

これは下記の問題を抱える:

1. **validation block が "実際に" 効いているかは検証できない**: regex で block の存在しか分からず、 `tofu plan` で本当に reject されるかは production apply まで判明しない。 codex review #4 で SA email の cross-repo hand-off を機械検証する gate を要求された
2. **同じ contract を 2 箇所で重複検査**: 例えば変数 `pubsub_dmail_inbound_subscription` の存在は terraform 側でも parse 時に check されるのに、 pytest でも regex で同じことを check していた (= 1 箇所修正で 2 箇所更新が必要)
3. **interpolation 解決後の値検査が自前実装**: `${var.project_id}` を `gen-ai-hironow` に置換する HCL_SUBSTITUTIONS 表を手で維持していた

調査の結果、 **2026 年 5 月時点で OpenTofu / Terraform の native test framework が GA** で、 `expect_failures = [var.xxx]` で variable validation block を実検証できる、 `assert var.xxx == "..."` で interpolation 解決後の値を直接 assert できる、 など pytest では届かなかった範囲を cover できることが分かった ([experiments/2026-05-06_iac-test-strategy.md](../../experiments/2026-05-06_iac-test-strategy.md))。

## Decision

**IaC 関連の test は責務で分割し、 同じ contract を 2 箇所で重複検査しない。** 各 framework の唯一無二の責務を持たせる:

### Layer 1 — `tofu test` (= variable validation 動的検証)

**配置**: 本リポ `tofu/tests/*.tofutest.hcl`、 dotfiles 側 `exe/coder/templates/dotfiles-devcontainer/*.tofutest.hcl`

**責務**:

- variable validation block の **実挙動** (`expect_failures = [var.xxx]`)
- variable の **default 値** (`assert var.xxx == "..."`)
- variable の **値 pattern** (`assert startswith(...)`、 `assert can(regex(...))`)
- `command = plan` で副作用ゼロ、 mock_provider + override_resource / override_data で実 GCP / Coder 接続不要

**例** (本リポ `tofu/tests/sa_validation.tofutest.hcl`):

```hcl
mock_provider "google" {}

run "control_plane_sa_email_is_rejected" {
  command = plan
  variables {
    exe_coder_vm_sa_email = "exe-coder@test-project.iam.gserviceaccount.com"
  }
  expect_failures = [var.exe_coder_vm_sa_email]
}
```

### Layer 2 — `pytest` (= heredoc body content + 外部 binary)

**配置**: 既存 `tests/exe/test_*.py` 流儀

**責務**:

- heredoc 内 shell script / systemd unit body の **string contains** (regex)
- forbidden literal scan (= hardcoded GCP identifier 等が unit body に直書きされていないこと)
- `systemd-analyze verify` / `bash -n` / `shellcheck` など **外部 binary** での検証 (terraform/tofu test の範囲外)
- e2e (Pub/Sub emulator + container 起動など、 実 docker run を伴う検証)

**例** (dotfiles `tests/exe/test_dmail_daemon_placement.py`):

```python
def test_dmail_units_have_no_hardcoded_gcp_identifiers(dmail_unit_bodies):
    forbidden = ("gen-ai-hironow", "dmail-inbound-receiver", ...)
    for unit, body in dmail_unit_bodies.items():
        for literal in forbidden:
            assert literal not in body
```

### 重複禁止

variable declaration / variable default の test は **Layer 1 のみ**。 pytest 側で同じ check を書かない。

heredoc body string contains / 外部 binary 検証は **Layer 2 のみ**。 tofu test 側で書こうとしない (terraform/tofu test では string contains 程度なら可能だが冗長で読みにくい)。

### CI 統合

- 本リポ: `just lint` に `just test-iac` (= `tofu test`) を組み込む
- dotfiles: `just check-all` に `test-iac` (= `tofu test` against Coder template) を組み込む
- pytest は既存 `just test` / `just check-all` で実行

## Consequences

### Positive

- **validation block が CI で機械検証される**: `expect_failures` で control-plane SA 誤指定が plan apply 前に止まる。 codex review #4 で指摘された cross-repo SA hand-off 単一障害点を解消
- **DRY**: 同じ contract を 1 箇所で test。 6 件の重複した pytest case を terraform test 側に retire できた
- **interpolation 検証が cheap**: HCL_SUBSTITUTIONS 自前テーブル不要、 `assert var.xxx == "..."` で 1 行
- **upstream 機能の進化に乗れる**: OpenTofu / Terraform native test に新機能が追加されたとき、 そのまま吸収可能

### Negative

- **学習コスト**: `*.tofutest.hcl` syntax を team 内で共有する必要 (本 ADR + experiments doc を ref として置く)
- **mock provider の auto-fill が provider 側 validation に hit するケースに individual override が要る**: 例として coder workspace data の id を `override_data` で固定する必要があった (DeepWiki opentofu/opentofu に既知の制約として記載)
- **layer 数増加**: pytest + tofu test + (e2e scaffold)。 ただし役割明確化で総 maintenance cost は減

### Neutral

- 既存 pytest を全廃する話ではない: `systemd-analyze verify` / `shellcheck` / 外部 binary を使う test は pytest にしか書けない
- `terraform test` ではなく `tofu test` に統一: dotfiles 側 Coder template でも `tofu test` を使う (lock file の registry が opentofu に固定されているため)。 Coder template の HCL は terraform/opentofu 互換なので動作問題なし

## 関連 ADR / docs

- ADR 0023 (dmail daemon distribution) — 本 ADR の test 対象を生んだ decision
- [`experiments/2026-05-06_iac-test-strategy.md`](../../experiments/2026-05-06_iac-test-strategy.md) — 各 framework の機能比較と採用判断の根拠
- 公式: [OpenTofu test command](https://opentofu.org/docs/cli/commands/test/) / [Terraform language/tests](https://developer.hashicorp.com/terraform/language/tests)
- DeepWiki: [opentofu/opentofu](https://deepwiki.com/opentofu/opentofu) / [hashicorp/terraform](https://deepwiki.com/hashicorp/terraform)
