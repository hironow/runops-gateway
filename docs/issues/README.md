# Issues

本リポと関連リポで残っている作業 (主に **本リポ範囲外** の cross-repo
依存) のチケット置き場。本リポ内で完結する作業は通常 ADR / handover で
管理する; ここは「他リポでの実装が伴うもの + 本リポでの後続処理」を
オープンチケットとして残すのが目的。

## Index

| # | Title | Repo / Owner | Status | Blocks |
|---|---|---|---|---|
| [0001](0001-dotfiles-dmail-receiver-systemd.md) | workspace VM に dmail-receiver / dmail-emitter を systemd unit + docker run として deploy | `hironow/dotfiles` + 本リポ Phase 2 | 🟡 Phase 1 GREEN (placement + tests)、 Phase 2 image build pending | 0002, 0003 |
| [0002](0002-five-pillars-frontmatter-trace.md) | 5本柱が D-Mail frontmatter から traceparent を読み span を再開 | 5本柱 4 リポ | 📝 未着手 | — |
| [0003](0003-phase3-outbound-enable.md) | Phase 3 outbound StreamingPull を実運用化 (`CLOUD_RUN_MIN_INSTANCES=1`) | `hironow/runops-gateway` + 0001 完了 | 📝 未着手 | — |
| [0004](0004-cloud-trace-span-verification.md) | Cloud Trace UI で実 span tree を確認 + 添付 | `hironow/runops-gateway` | 📝 未着手 | — |
| [0005](0005-async-span-flush.md) | goroutine 内 span が Cloud Run idle shutdown までに flush されず lost する | `hironow/runops-gateway` | ✅ GREEN 完了 (PendingTracker + ordered shutdown + semgrep rule) | — |

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
