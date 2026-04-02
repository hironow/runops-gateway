# 0003. Cloud Run の CPU スロットリング無効化

**Date:** 2026-04-02
**Status:** Accepted

## Context

Cloud Run はデフォルトで HTTP レスポンスを返した直後に CPU 割り当てをほぼゼロ（スロットリング）にする。
ADR 0002 の非同期 Goroutine パターンを採用した場合、`200 OK` 返却後にバックグラウンドの
Goroutine（LRO 待機・Slack 更新）が CPU 不足で凍結・著しく遅延するという致命的なバグが発生する。

## Decision

runops-gateway の Cloud Run サービスは、必ず以下の設定でプロビジョニングする:

```
run.googleapis.com/cpu-throttling = "false"
```

（OpenTofu の `annotations` で設定。ADR 0001 で定めた runops-gateway のみに適用する）

## Consequences

### Positive
- Goroutine が HTTP レスポンス後も安定して動作する
- LRO の待機が凍結せずに正常完了する

### Negative
- HTTP リクエスト処理外でも CPU が割り当てられるため、アイドル時のコストが若干増加する
- 最小インスタンス数 1 以上を設定すると常時コストが発生する

### Neutral
- 最小インスタンス数 0（コールドスタート許容）であれば実質的なコスト増は軽微
