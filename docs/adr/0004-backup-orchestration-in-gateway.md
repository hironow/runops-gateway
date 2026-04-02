# 0004. DB バックアップのオーケストレーションを Gateway に集約

**Date:** 2026-04-02
**Status:** Accepted

## Context

DB マイグレーション実行前に Cloud SQL のオンデマンドバックアップを取得する要件がある。
実装の選択肢は 2 つある:

1. マイグレーション Job コンテナ内でバックアップ API を叩く
2. runops-gateway がバックアップ完了後に Job をキックするオーケストレーターとなる

選択肢 1 の問題点:

- Job コンテナに `cloudsql.admin` という強い権限が必要になり、最小権限の原則に反する
- Slack への進捗フィードバックが難しい（Job 内部のどのフェーズにいるか外から見えない）

## Decision

バックアップのトリガーと完了待機は runops-gateway（インフラ管理層）が担う。
完了後に Cloud Run Jobs をキックするオーケストレーション方式を採用する。

マイグレーション Job コンテナには DB クライアント権限のみを付与する。

## Consequences

### Positive

- 最小権限の原則（Least Privilege）を遵守できる
- 各フェーズ（バックアップ中/マイグレーション中）を Slack に個別にフィードバックできる
- Job の失敗とバックアップの失敗を独立してハンドリングできる

### Negative

- runops-gateway のサービスアカウントに `cloudsql.admin` が必要になる
- オーケストレーションロジックが gateway の UseCase に入るため複雑度が上がる

### Neutral

- 将来、Cloud Workflows に移行する場合は UseCase の分離が活きる
