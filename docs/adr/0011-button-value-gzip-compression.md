# 0011. Slack ボタン値の常時 gzip 圧縮

**Date:** 2026-04-02
**Status:** Accepted

## Context

Slack の Block Kit では button 要素の `value` フィールドに最大 **2,000 文字** の制限がある。
複数リソースを CSV で束ねた `ApprovalRequest` (ADR 0010) を JSON シリアライズすると、
サービス数・リビジョン名の長さによっては 2,000 文字を超えるケースが発生した。

### 検討した選択肢

| 案 | 内容 |
|----|------|
| A | 2,000 文字超過時のみ圧縮する（条件分岐） |
| B | 常に圧縮する（無条件） |

### 案 A の問題点

- **バグ発見の遅延**: 圧縮パスが「大規模バンドル時のみ」実行されるため、デコード実装にバグがあっても通常テストでは検出されない
- **テストの網羅性**: `parseActionValue` のデコードブランチが本番相当データでしか踏まれない
- **境界の複雑さ**: 「何文字から圧縮するか」という閾値の管理が発生する

## Decision

**ボタン値を常に（無条件で）gzip + base64url 圧縮する。**

```
encode: JSON → gzip → base64url (RawURLEncoding) → "gz:" + base64url_string
decode: "gz:" prefix 検出 → base64url decode → gunzip → JSON parse
```

- `compressButtonValue(s string) string` — Go 側（`blockkit.go`）
- `compress_gz()` — bash 側（`scripts/notify-slack.sh`）

両者のアルゴリズムを揃えることで、Cloud Build スクリプトが送信したボタンを
Go ハンドラが確実にデコードできることを保証する。

### フォールバック設計

gzip 圧縮後も 2,000 文字を超える場合（エントロピーが極めて高いサービス名を大量に持つ稀なケース）:
- 圧縮値はそのまま返す（無言でボタンを壊さない）
- `buttonValueError` がサイズを事前チェックし、超過していれば **専用エラーメッセージを Slack に投稿** する

### 後方互換性

旧フォーマット（生 JSON、`gz:` プレフィックスなし）も `parseActionValue` で引き続きパース可能。
既に Slack に投稿済みの旧ボタンを誤作動させない。

## Consequences

### Positive
- `parseActionValue` のデコードパスが全てのボタンクリックで実行される → バグをテスト・本番両方で早期検出
- テスト `TestNotifyScript_CompressGz_CompatibleWithParseActionValue` で bash/Go 間のラウンドトリップを保証
- 通常サイズのペイロードも圧縮されるため、ボタン値が実質的にサイズ上限に近づかない

### Negative
- 圧縮/展開の CPU コストが全ボタンクリックに発生する（マイクロ秒オーダーで無視可能）
- 生 JSON で目視デバッグできなくなる（`echo "<gz:...>" | cut -c4- | base64 -d | gunzip` で復元可能）

### Neutral
- bash の `compress_gz` と Go の `compressButtonValue` は同一アルゴリズムを実装しており、
  `TestNotifyScript_CompressGz_CompatibleWithParseActionValue` がクロス言語の整合性を継続的に保証する

## 関連 ADR

- ADR 0010: Multi-Resource Deployment（CSV バンドルで 2,000 文字制限が顕在化した背景）
