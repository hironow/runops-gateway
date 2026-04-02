# 0005. Ports and Adapters パターンの採用

**Date:** 2026-04-02
**Status:** Accepted

## Context

Slack を唯一の入力インターフェースとして設計した場合、以下の問題が生じる:

- 緊急時に CLI からオペレーションを実行できない
- UseCase のユニットテストに Slack の HTTP ペイロードが必要になり、テストが複雑化する
- 将来的に別の入力（Web UI、Backstage 等）への対応コストが高い

## Decision

Hexagonal Architecture（Ports and Adapters パターン）を採用し、以下の層に厳密に分離する:

- **Core Domain / Ports**: 外部依存ゼロのインターフェース定義
- **UseCase**: コアビジネスロジック（Port インターフェースのみに依存）
- **Driving Adapters**: Slack HTTP Handler、CLI（Cobra）
- **Driven Adapters**: GCP API クライアント、Slack 通知クライアント、Auth Checker

Slack と CLI はどちらも `domain.ApprovalRequest` という単一の構造体を UseCase に渡す。

## Consequences

### Positive
- UseCase のユニットテストが純粋な Go 構造体のみで記述できる
- CLI から同一のオペレーションが実行可能になる
- 将来の入力追加コストが低い（新 Adapter を実装するだけ）

### Negative
- 初期の実装ボリュームが若干増える（インターフェース定義が必要）
- 薄い Adapter 層が増えるため、コードの見通しがやや複雑になる

### Neutral
- ディレクトリ構造が Hexagonal Architecture を反映した形になる
