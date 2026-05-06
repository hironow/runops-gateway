# Experiment: IaC test strategy (OpenTofu + Terraform native test, 2026-05)

**Date:** 2026-05-06
**Objective:** Issue 0001 着手で書いた IaC 周りの test (pytest + regex で main.tf を text マッチ + heredoc 抽出 + systemd-analyze verify) について、 2026-05 時点の OpenTofu / Terraform native test 機能を踏まえて「もっと良い方法」 がないかを調査し、 採用方針を確定する。
**Status:** 🟢 Complete (採用方針確定、 実装着手)

## Background

Issue 0001 Phase 1 で書いた dotfiles 側 test (`tests/exe/test_dmail_daemon_placement.py`) は:

1. `main.tf` を Python で読む
2. regex で variable 宣言 / heredoc 抽出 / 内容 contains を assert
3. 抽出した systemd unit body を Docker container 内で `systemd-analyze verify` に渡して syntax check

これは codex review でも「contract を pin できる」 と評価されたが、 **terraform / opentofu native の test 機能** が 2026 年に GA レベルで使えるなら、 その方が:

- variable validation block を実 plan で検証できる (= runops-gateway 側 `c7ce7c4` で追加した SA email pattern validation)
- variable interpolation 後の値を直接 assert できる
- Terraform module 単位で CI と integrate しやすい

という利点が見込める。 採用判断のため最新仕様を調査。

## Sources (公式 + DeepWiki)

- [OpenTofu `tofu test` docs](https://opentofu.org/docs/cli/commands/test/) — 1.11.x 時点の機能
- [Terraform `terraform test` language](https://developer.hashicorp.com/terraform/language/tests) — 1.15.x 時点の機能 (1.6 GA、 1.7 で mock provider 追加)
- [DeepWiki opentofu/opentofu](https://deepwiki.com/opentofu/opentofu) — 実装内部 (mock_provider / override / expect_failures)
- [DeepWiki hashicorp/terraform](https://deepwiki.com/hashicorp/terraform) — 同上、 Terraform 1.7+ feature

## 調査結果

### OpenTofu (1.11.x — runops-gateway / dotfiles tofu/exe で使用)

| 機能 | サポート |
|---|---|
| `tofu test` command + `*.tofutest.hcl` (or `.tftest.hcl`) | ✅ GA |
| `mock_provider` (computed 自動生成) | ✅ |
| `override_resource` / `override_data` / `override_module` | ✅ |
| `expect_failures = [var.xxx]` で variable validation 検証 | ✅ |
| `command = plan` (副作用なし) | ✅ |
| `assert { condition = ... }` で string content 検査 | ✅ (string equality / contains 等) |
| heredoc 内 embedded shell script の **直接** content test | ❌ (assert 経由で string match は可能) |
| 外部 binary (systemd-analyze, docker build) 経由の test | ❌ (test framework の範囲外) |

### Terraform (1.15.x — dotfiles の Coder template が使用、 Coder server が実行)

| 機能 | サポート |
|---|---|
| `terraform test` + `*.tftest.hcl` | ✅ GA (1.6+) |
| `mock_provider` | ✅ (1.7+) |
| `override_resource` / `override_module` | ✅ |
| `expect_failures = [var.xxx]` | ✅ |
| `assert { condition = startswith(...), contains(...), regex(...) }` | ✅ |
| Coder workspace template 専用 lint | ❌ (general terraform test として使う) |

### 既存 pytest+regex 流儀との比較

| Test 内容 | pytest+regex (現状) | tofu test / terraform test |
|---|---|---|
| variable 宣言の存在 | ◯ regex | ✅ variables block で参照、 missing なら parse fail |
| **variable validation block の挙動** (= `c7ce7c4` の SA pattern) | ❌ (regex で block 存在のみ check、 実 validation 動作は確認できない) | **✅ `expect_failures = [var.exe_coder_vm_sa_email]` で実 validation 検証** |
| variable interpolation 後の値 | ◯ HCL_SUBSTITUTIONS 自前 | ✅ `assert var.xxx == "..."` で直接 |
| systemd unit body の string contains | ◯ regex | △ assert で `contains(local.startup_script, "...")` が可能だが冗長 |
| **systemd-analyze verify (外部 binary)** | ✅ docker 内で実行 | ❌ test framework の範囲外 |
| terraform plan が syntax error なく成功すること | △ (既存 `tests/exe/test_template.py` で `terraform validate`) | ✅ `command = plan` 自体が成功条件 |

## 採用方針: 役割分担 (重複ゼロ)

各 test framework に **唯一無二の責務** だけを持たせ、 同じ contract を 2 箇所で重複検査することは避ける。

### Layer 1 — `tofu test` (runops-gateway 側、 NEW)

**唯一の責務**: variable validation block の **実挙動** 検証。 `tofu/tests/sa_validation.tofutest.hcl` で `exe_coder_vm_sa_email` validation block を `expect_failures` でテスト:

- `exe-coder@...` (control plane SA 誤設定) → plan 失敗
- `exe-workspace@...` (workspace SA) → plan 成功
- `""` (bootstrap 期、 empty 許容) → plan 成功

これは現行 pytest では不可能 (validation block の存在は regex で見えても、 実際に reject されるかは tofu plan が必要)。

### Layer 2 — `terraform test` (dotfiles Coder template 側、 NEW)

**唯一の責務**: variable の **宣言と default 値** の正しさ。 `exe/coder/templates/dotfiles-devcontainer/dmail.tftest.hcl` で:

- 4 新 runtime variable (`pubsub_dmail_inbound_subscription` / `pubsub_dmail_outbound_topic` / `otel_exporter_otlp_endpoint` / `otel_traces_sampler_arg`) の default が production と一致 (`assert var.xxx == "..."`)
- 2 image variable (`dmail_receiver_image` / `dmail_emitter_image`) の default が AR repo path 形式 (`startswith(var.xxx, "asia-northeast1-docker.pkg.dev/")`)

これは pytest でも `regex variable\s+"..."` で書けるが、 terraform test の方が natural (parse 済 HCL を直接参照)。 **同じ contract を 2 箇所で持たない** ため、 移管後は対応する pytest test を retire。

### Layer 3 — pytest (dotfiles 既存、 縮小して維持)

**唯一の責務**: terraform native test では検査不能 / 冗長な領域に絞る:

- **heredoc 内 systemd unit body** の string contains (ExecStart= / Restart=on-failure / volume mount path 等)
- **hardcode 検出** (forbidden literal scan、 `gen-ai-hironow` などが unit body に直書きされていないこと)
- **systemd-analyze verify** (外部 binary、 terraform test 範囲外)
- e2e scaffold (Pub/Sub emulator + container 起動、 Phase 3 後に有効化)

### Retire (pytest から移管 / 削除)

terraform test に責務を移すので pytest から削除する 6 件:

- `test_main_tf_declares_dmail_receiver_image_variable`
- `test_main_tf_declares_dmail_emitter_image_variable`
- `test_main_tf_declares_dmail_runtime_variable[pubsub_dmail_inbound_subscription]`
- `test_main_tf_declares_dmail_runtime_variable[pubsub_dmail_outbound_topic]`
- `test_main_tf_declares_dmail_runtime_variable[otel_exporter_otlp_endpoint]`
- `test_main_tf_declares_dmail_runtime_variable[otel_traces_sampler_arg]`

retire 後の構成:

| Layer | Cases | 検査対象 |
|---|---|---|
| `tofu test` | 3 (or more) | validation block の実挙動 |
| `terraform test` | 6 | variable 宣言 + default 値 |
| pytest 残存 | 11 + 2 skipped | heredoc body content + systemd-analyze + e2e scaffold |

合計 20 effective coverage (= 17 → 20 で増、 重複ゼロ)。

### CI 統合

- **runops-gateway**: `just lint` に `tofu test` を統合。 GitHub Actions CD workflow の `infra` job 前に gate として実行 → SA validation 不整合は plan apply 前に止まる
- **dotfiles**: `just check-all` に `terraform test` を統合。 pytest と並列実行可能 (Docker と独立、 各々 < 5 秒)

## 実装計画

1. **runops-gateway**: `tofu/tests/sa_validation.tofutest.hcl` を新規作成 (validation block の expect_failures + workspace SA pattern accept の 2 ケース)
2. **dotfiles**: `exe/coder/templates/dotfiles-devcontainer/dmail.tftest.hcl` を新規作成 (4 新 variable の default + image variable の placeholder pattern assert)
3. **runops-gateway**: `justfile` に `tofu test` を組み込む (pre-commit / CI で実行)
4. **dotfiles**: `justfile` に `terraform test` を組み込む (`just check-all` の流れに乗せる)
5. **既存 pytest** は維持 (Layer 3 = 外部 binary が必要な test は terraform test では代替不能)

## Trade-offs

### Positive

- **validation block の "効いている" 確証**: 現状 regex で block 存在は分かるが、 実 plan でどう振る舞うかは production apply まで分からない。 `expect_failures` で CI に gate できる
- **interpolation 解決後の値検査が cheap**: `assert var.xxx == "..."` は HCL_SUBSTITUTIONS 自前メンテよりシンプル
- **terraform / opentofu native** = upstream の進化に乗れる、 mock_provider など追加機能を取り込める
- **CI integrate が容易**: `just test-iac` 1 コマンドで terraform 系 test 全実行
- **2 layer で同じ contract を pin**: pytest が静的、 tofu/terraform test が動的、 一方が腐っても他方が catch

### Negative

- **学習コスト**: `*.tftest.hcl` syntax を team 内で覚える必要 (ただし ADR / experiments で参照する流儀)
- **OpenTofu と Terraform で `mock_provider` の対応が微妙に違う**: provider-defined functions の mock は OpenTofu で限定的 (DeepWiki 出典)。 今回の test 範囲では関係しない
- **maintain layer 数増加**: pytest 維持 + tofu test 追加 + terraform test 追加 = 3 layer

### Neutral

- 既存 pytest test (17 cases) を retire する話ではない。 native test を **追加** する形なので net 増加
- `terraform test` / `tofu test` は実 infra を作らない (`command = plan`) ので CI コスト無視できる

## 結論

採用する。 Layer 1 / Layer 2 を新規追加、 Layer 3 (既存 pytest) は維持。 SA email validation block + 4 新 variable default を terraform native test で gate することで、 codex review #4 で指摘された "cross-repo SA hand-off の人手依存" が CI 段階で機械的にブロックされる構造に進化。

## 関連

- ADR 0023 (dmail daemon distribution、 本 experiments の test 対象を生んだ decision)
- Issue 0001 Phase 1 (dotfiles) + Phase 2 (runops-gateway image build)
- `experiments/2026-05-06_dotfiles-dmail-daemon-placement.md` (placement 設計)
- codex review session (2026-05-06、 4 round で clear)
