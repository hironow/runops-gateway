# Experiments

実装に入る前の調査・予備実験ノート置き場。CLAUDE.md の `<experiments-guidelines>` に従う。

## Index

| Date | Experiment | Status | Note |
|---|---|---|---|
| 2026-05-05 | [otel-cloud-run-pubsub-jaeger](2026-05-05_otel-cloud-run-pubsub-jaeger.md) | 🟢 Complete | Phase 4b と並行で OTel 配線するためのベスプラ調査。次の ADR 0020 (Direct OTLP export) のインプット |
| 2026-05-05 | [cloudevents-adoption](2026-05-05_cloudevents-adoption.md) | 🟢 Complete | CloudEvents 採用検討。結論「不採用 (現状維持)」、再検討トリガー条件は ADR 0022 で記録 |

## ステータス凡例

- 🟢 Complete — 結論が出た。実装手順が ADR / Issue に落ちている
- 🟡 In Progress — 調査・実験中
- ⚪ Not Started — 計画のみ
