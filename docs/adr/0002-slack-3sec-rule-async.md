# 0002. Slack 3秒ルールの回避 - 非同期処理と response_url

**Date:** 2026-04-02
**Status:** Accepted

## Context

Slack の Interactive Payload（ボタン押下時）は、3 秒以内に HTTP 200 OK を返さないと
Slack UI 上に「エラー」が表示される。

一方、GCP リソースの操作（LRO: Long Running Operation）は完了まで数分かかる場合がある。
同期的に LRO の完了を待ってからレスポンスを返す設計は必ず破綻する。

## Decision

- HTTP ハンドラはリクエスト受信後、Goroutine で処理を非同期に逃がし、即座に `200 OK` を返す
- LRO の進捗・結果は、Slack のペイロードに含まれる `response_url` を使って元のメッセージを逐次上書き（`replace_original: true`）することで UX を担保する
- Goroutine には `context.Background()` を渡し、HTTP リクエストの context に依存させない

## Consequences

### Positive

- Slack 上でのエラー表示を回避できる
- LRO の進捗をリアルタイムにフィードバックできる

### Negative

- Goroutine のライフサイクル管理が必要（CPU スロットリング問題 → ADR 0003 参照）
- HTTP レスポンス後のエラーはユーザーへの通知手段を別途確保する必要がある

### Neutral

- `response_url` の有効期限は 30 分のため、長時間処理の場合は考慮が必要
