# CloudEvents 採用検討 (Cloud Pub/Sub + OTel との組み合わせ, 2026年5月)

**Date:** 2026-05-05
**Objective:** 既に OpenTelemetry + Cloud Pub/Sub で動いている D-Mail bridge に CloudEvents を導入することで「コード量が減る」「将来拡張性が上がる」のどちらか/両方が成立するかを評価し、採用/不採用を判断する。
**Status:** 🟢 Complete (結論は ADR 0022 で記録)

## Background

ADR 0013 (Pub/Sub bridge), ADR 0021 (`EnableOpenTelemetryTracing` 委譲) を踏まえ、Pub/Sub message attribute schema を CloudEvents v1.0 に寄せる選択肢を再検討する。Eventarc trigger 連携や他システム (GitHub Actions / Cloud Workflows) との相互運用性が将来要件として浮上する可能性があるため、今のうちに採用根拠を整理する。

## Hypothesis

1. CloudEvents v1.0 spec は stable で Go SDK も production-ready
2. ただし本リポは **Pub/Sub を 1 hop しか跨がない** + **5本柱は Markdown frontmatter を読む** ため、CloudEvents の主たるメリット (Eventarc 連携 / multi-hop trace 起点保持 / spec 標準化) が活かしにくい
3. ADR 0021 の `googclient_traceparent` 一本化と CloudEvents distributed-tracing extension の `ce-traceparent` が衝突する可能性

## 結論 (一行)

**不採用 (現状維持)**。SDK の依存と spec の安定性は production-ready だが、本リポでは「コード量が減る」も「将来拡張性が上がる」も実質成立せず、ADR 0021 の `EnableOpenTelemetryTracing` 自動 inject と CloudEvents distributed-tracing extension の境界が曖昧になるリスクの方が大きい。

---

## 1. CloudEvents v1.0 spec の現状 (2026/05)

- **最新 stable: v1.0.2** (2024-02-06 リリース、`ce@v1.0.2` タグ)。2024 年以降、spec 本体に新しい release は出ていない。`main` ブランチには `1.0.3-wip` 表記があるが未リリース
- CNCF **Graduated project** に昇格 (2024-01-25)。プロジェクトとしては成熟・stable だが「動きが止まっている」面もある
- **REQUIRED context attributes** (4 つ): `id`, `source` (URI-reference), `specversion` ("1.0"), `type`
- **OPTIONAL**: `subject`, `time` (RFC 3339), `datacontenttype` (RFC 2046), `dataschema`
- **本リポ独自 attribute との対応関係**:

| 自前 schema (ADR 0013) | CloudEvents 対応 | 評価 |
|---|---|---|
| `kind` (specification/...) | `type` (e.g. `dev.runops-gateway.dmail.specification.v1`) | 直接 1:1 mapping 可 |
| `target_tool` (paintress/...) | `subject` または `partitionkey` 拡張 | `subject` は spec 上「event の主題」なので意味が近い |
| `source` (runops-gateway-slack/...) | `source` | 1:1。ただし URI-reference 形式必須 (`//runops-gateway/slack` 等) |
| `idempotency_key` (SHA-256) | `id` | spec 上「producer scope で unique」なので idempotency key と意味的にほぼ同じ |
| `dmail_schema_version` ("1") | extension or `type` 末尾の `.v1` | 拡張 attribute or type 命名で表現 |
| `slack_channel_id`, `slack_thread_ts`, `parent_idempotency_key`, `requester_id`, `severity` | 全て extension attribute | 任意拡張可能 (英小文字+数字のみ等の制約あり) |

- **Extensions の運用**: 任意名で追加可能だが、attribute 名は **英小文字 + 数字のみ** (アンダースコア禁止)。本リポの `idempotency_key`, `target_tool`, `slack_channel_id`, `slack_thread_ts`, `parent_idempotency_key`, `requester_id`, `dmail_schema_version` は **全て CloudEvents attribute 命名規則違反** (アンダースコア使用)。CloudEvents 採用するなら全部 rename が必要 (例: `idempotencykey`, `targettool`, `slackchannelid`...)。これは **既存 ADR 0013 schema を全部書き直す必要がある** 致命点に直結

参照: [cloudevents/spec v1.0.2](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md)、[cloudevents.io](https://cloudevents.io/)

---

## 2. Go SDK の現状 (2026/05)

- **`github.com/cloudevents/sdk-go/v2` 最新: v2.16.2** (2025-09-22 リリース)。安定的に release が回っている
- **Pub/Sub binding**: 2 つ並存
  - `github.com/cloudevents/sdk-go/protocol/pubsub/v2` (新): `cloud.google.com/go/pubsub/v2 v2.6.0` 依存。**本リポと同じ v2 系列**で整合する。Go 1.25.7 以上必須
  - `github.com/cloudevents/sdk-go/v2/protocol/pubsub` (旧): `cloud.google.com/go/pubsub` v1 依存。本リポと不整合
- **`WithClient(client *pubsub.Client) Option`**: ユーザーが構築済みの v2 client を流し込める。**つまり ADR 0021 の `EnableOpenTelemetryTracing: true` を設定した client を CloudEvents Protocol に渡せる**。これは重要 (後述)
- **OpenTelemetry の自動有効化はしない**。OTel は別モジュール `github.com/cloudevents/sdk-go/observability/opentelemetry/v2` で `OTelObservabilityService` / `InjectDistributedTracingExtension` / `ExtractDistributedTracingExtension` を提供
- **API 概略**:
  - 送信: `protocol.Send(ctx, event)` — 内部で binary mode で `ce-*` attribute を Pub/Sub message attribute に詰める
  - 受信: `protocol.Receive(ctx)` → `*event.Event` を返す
  - **Binary mode が default**

参照: [pkg.go.dev sdk-go/protocol/pubsub/v2](https://pkg.go.dev/github.com/cloudevents/sdk-go/protocol/pubsub/v2)、[sdk-go/releases](https://github.com/cloudevents/sdk-go/releases)、[observability/opentelemetry/v2](https://pkg.go.dev/github.com/cloudevents/sdk-go/observability/opentelemetry/v2)

---

## 3. Cloud Pub/Sub との結合

- **Google Cloud Pub/Sub Protocol Binding for CloudEvents** (`github.com/googleapis/google-cloudevents/blob/main/docs/spec/pubsub.md`) は **「working draft」** 表記が残っており、CloudEvents 本体 spec のような stable release tag は付いていない。これは spec 採用判断における中程度のリスク
- **Binary content mode**: CloudEvents context attribute は `ce-` prefix で Pub/Sub `Attributes` に格納、event data は `Data` にそのまま入る
- **Eventarc 経由配信**: `ce-id`, `ce-source` (`//pubsub.googleapis.com/projects/.../topics/...`), `ce-specversion`, `ce-type` (`google.cloud.pubsub.topic.v1.messagePublished`), `ce-time` を HTTP header で受信側に渡す。**ただしこれは Eventarc trigger を Pub/Sub topic に張った場合の話**。本リポは StreamingPull で直接 subscribe しているので Eventarc は介在せず、`ce-*` header は自動付与されない
- **直接 publish で CloudEvents binding を使うには**: `cloudevents/sdk-go/protocol/pubsub/v2` を使って自前で publish するか、**手で `Attributes` に `ce-*` を詰める** かの 2 択

参照: [google-cloudevents/docs/spec/pubsub.md](https://github.com/googleapis/google-cloudevents/blob/main/docs/spec/pubsub.md)、[Eventarc CloudEvents format](https://docs.cloud.google.com/eventarc/docs/cloudevents) (last updated 2026-05-01)

---

## 4. OTel との重複・補完関係

ここが本リポでの一番大きな技術的論点。

- **CloudEvents Distributed Tracing Extension**:
  - 定義する attribute: `traceparent` (required), `tracestate` (optional)
  - 明示的に: 「The Distributed Tracing Extension is **not intended to replace** the protocol specific headers for tracing, like the ones described in W3C Trace Context for HTTP.」
  - Single-hop の場合: extension は protocol header と **同じ** trace context を持たねばならない
  - Multi-hop の場合: extension は **starting trace** (起点の trace) を保持、protocol header は hop-by-hop の trace
- **本リポの現状 (ADR 0021)**:
  - `cloud.google.com/go/pubsub/v2 v2.5.1+` の `EnableOpenTelemetryTracing: true` で `googclient_traceparent` を message attribute に自動 inject
  - publisher span / subscriber span (`messaging.gcp_pubsub.*` semconv) も自動生成
- **CloudEvents 採用時の trace context 状況**:

| 経路 | Protocol header (transport-level) | CloudEvents extension |
|---|---|---|
| 現状 | `googclient_traceparent` (library 自動) | 無し |
| CloudEvents 採用 | `googclient_traceparent` (library 自動、消せない) | `ce-traceparent` を sdk-go observability で別途 inject |

- **問題点**: ADR 0021 で ADR 0013 の手動 `traceparent` を消した直後に、今度は CloudEvents extension の `ce-traceparent` を入れることになり、**実質「ADR 0021 を半分巻き戻す」格好になる**
- 本リポは **Pub/Sub を 1 hop しかしない** ので、起点 trace と hop-by-hop が一致する。CloudEvents extension の存在意義 (multi-hop で起点を保持) が **発生しない**

参照: [CloudEvents Distributed Tracing Extension](https://github.com/cloudevents/spec/blob/main/cloudevents/extensions/distributed-tracing.md)、[Pub/Sub OpenTelemetry Tracing](https://docs.cloud.google.com/pubsub/docs/open-telemetry-tracing)、本リポ ADR 0021

---

## 5. 本リポへの導入評価

### メリット

1. **`ce-type` で kind を表現できる** → type-safe な意味付け (`dev.runops-gateway.dmail.specification.v1`)。ただし現状 `kind` も既に enum で型安全 (`ParseDMailKind`)
2. **将来 Eventarc trigger を張りたくなったとき**、`ce-*` attribute が既に Pub/Sub message に乗っていると Eventarc の Cloud Run trigger が自然に動く。**ただし本リポは Cloud Run → Pub/Sub → exe-coder VM (StreamingPull) の構成で、exe-coder は Eventarc では呼べない** (Eventarc は HTTP target 限定、systemd daemon は対象外)
3. **他システム (GitHub Actions / Cloud Workflows) との相互運用性**: 将来 GitHub Actions が直接 D-Mail を投げる構想があれば CloudEvents で揃える価値はある。**現状そういう計画は intent.md / handover.md に存在しない**
4. **schema を spec に寄せる** ことで「外部協調」が必要な場面で説明コストが下がる

### デメリット

1. **既存 ADR 0013 schema の全 attribute を rename 必要** (CloudEvents は attribute 名が英小文字+数字のみ; 本リポは underscore 付き)。production 動作中の publisher/subscriber を **両端同時** に切り替える必要があり、リスク・調整コストが大きい
2. **新規依存**: `github.com/cloudevents/sdk-go/v2` + `github.com/cloudevents/sdk-go/protocol/pubsub/v2` + (場合により) `github.com/cloudevents/sdk-go/observability/opentelemetry/v2`。3 modules 追加
3. **5本柱は CloudEvents を読まない**。phonewave outbox の `.md` ファイルに書くのは receiver 側の責務で、5本柱は frontmatter (`dmail-schema-version: "1"` 等) を読むだけ。**つまり CloudEvents は wire format でしか役立たず、`RenderMarkdown()` / `ParseDMail()` の frontmatter schema は CloudEvents に変えられない**。receiver で「CloudEvents → frontmatter」のマッピング層を新規実装する必要がある
4. **ADR 0021 の決定とコンセプトが食い違う**。ADR 0021 は「trace は library に委譲、attribute schema を最小化」。CloudEvents は逆方向 (attribute schema を肥大化、trace を extension で再導入) なので、設計思想として整合しない
5. **Pub/Sub binding spec が "working draft"**。CloudEvents 本体は v1.0.2 で stable だが、Google 側の Pub/Sub binding doc は draft 表記が外れていない

### コード量への影響 (定量評価)

- **削減量**: 自前 attribute set/get の **~25 行** が消える (publisher.go の attrs map 構築)
- **追加量**:
  - `ev.SetXxx` 系 ~12 行
  - `cloudevents/sdk-go/protocol/pubsub/v2` の `Protocol` 構築・`WithClient` で既存 client 注入 ~8 行
  - receiver 側で `ev.Extensions()` から自前 schema へ rename mapping ~15 行
  - `RenderMarkdown()` / `ParseDMail()` は 5本柱の都合で残る (削れない)
  - **新規 dependency 3 module + Go module graph 肥大化**
- **正味**: コード量はほぼ ±0 〜 微増 (5-15 行増)。**「コード量が減る」は成立しない**

### 拡張性への影響

- **増える拡張性**:
  - 将来 Eventarc trigger を張れば Cloud Run target で `ce-*` header が自動で揃う (が、本リポの target は Cloud Run **発信** 側で、受信は exe-coder VM systemd なので Eventarc trigger には乗れない → **実質メリット無し**)
  - CloudEvents SQL での routing/filter (cloudevents.io 2024-06 リリースの V1 機能) — Pub/Sub の attribute filter で同じことができる
- **減る拡張性**:
  - schema 変更時に「自前 ADR 改訂 vs CloudEvents extension 追加」の選択肢が増えて意思決定コストが上がる
  - 5本柱の Markdown frontmatter schema が CloudEvents と分岐し続けるので「2 種類の serialization を receiver で扱う」固定費が増える

---

## 6. Eventarc との関係

- **直接 Pub/Sub publish (現状)**: subscription を自前で持ち、StreamingPull で pull する。IAM は `roles/pubsub.subscriber` で完結
- **Eventarc 経由**: Eventarc trigger を Pub/Sub topic に張り、target を Cloud Run service にする。**ただし target は HTTP endpoint 限定**。本リポの dmail-receiver は exe-coder VM 上の systemd daemon (HTTP server ではない) なので **Eventarc では呼べない**。Eventarc に移行するには receiver を HTTP server 化 + Cloud Run 化 + tsnet で exe-coder VM に push する別経路... と、ADR 0013 で **却下した「案 A (HTTP receiver)」と同じ世界観に戻る**
- **本リポ専用結論**: Eventarc 導入 = ADR 0013 の前提を覆す。CloudEvents だけ採用しても Eventarc には載れないので、CloudEvents の Eventarc 連携メリットは **本リポでは 0**

参照: [Eventarc Advanced direct publishing](https://docs.cloud.google.com/eventarc/advanced/docs/publish-events/publish-events-direct-format) (last updated 2026-05-01)、[Eventarc CloudEvents format](https://docs.cloud.google.com/eventarc/docs/cloudevents)

---

## 採用判断テーブル

| 観点 | 現状維持 (自前 schema) | CloudEvents 完全採用 | 部分採用 (envelope のみ、trace ext 無し) |
|---|---|---|---|
| spec 安定度 | 自前 (手元で固定) | CloudEvents v1.0.2 stable, GCP binding は "working draft" | 同左 |
| 依存追加 | 0 | sdk-go/v2 + protocol/pubsub/v2 + observability/opentelemetry/v2 | sdk-go/v2 + protocol/pubsub/v2 |
| ADR 0013 schema 影響 | 影響なし | 全 attribute rename (underscore 不可)、両端同時切替 | 同左 |
| ADR 0021 trace 整合性 | 整合 | `googclient_*` と `ce-traceparent` の二重 inject、ADR 0021 と矛盾 | `googclient_*` のみで OK (整合) |
| RenderMarkdown / ParseDMail | 影響なし | 5本柱は CloudEvents 読まないので残置 (2 種 serialization) | 同左 |
| Eventarc 連携の余地 | 無し | あるが exe-coder receiver で活かせない | 同左 |
| コード量 | baseline | 微増 (5-15 行) | 微減〜±0 |
| Pub/Sub message size | 現状 | underscore 除去で軽微増、`ce-*` prefix で attribute 数は同等 | 同左 |
| 他システム互換性 | 自前で説明必要 | spec 準拠と言える | 中途半端 |
| 移行リスク | 0 | 高 (両端同時切替 + dedup key の id 化) | 中 |

---

## 注意点 / 罠

1. **`idempotency_key` を `ce-id` に流用する罠**: CloudEvents `id` は producer scope で unique (= 同じ event の retry なら同じ id) という意味で idempotency と整合する。しかし本リポの `idempotency_key` は SHA-256 で内容由来なので、retry すると同じ key が来る。これは仕様上 OK だが、Pub/Sub の `messageId` (publisher が生成する別 ID) と混同しないこと
2. **CloudEvents attribute 命名規則**: 英小文字 + 数字のみ、underscore/hyphen 不可。本リポの `target_tool` `idempotency_key` `slack_channel_id` `slack_thread_ts` 等は全部 rename が必要
3. **ADR 0021 で消した `traceparent` を CloudEvents distributed-tracing extension で実質復活させるな**。spec 上「補完」だが、本リポの 1-hop 構成では不要
4. **`cloudevents/sdk-go/protocol/pubsub/v2` の `WithClient(*pubsub.Client)`** で `EnableOpenTelemetryTracing: true` の client を流し込めるので、OTel tracing は library に任せたまま CloudEvents serialization だけ載せる構成は技術的に可能。これが「部分採用」の唯一の正解パスだが、5 章の通りメリットが薄い
5. **Pub/Sub binding spec の draft 表記**: Google `google-cloudevents` の `pubsub.md` は今でも "working draft"
6. **5本柱は CloudEvents を読まない**。CloudEvents 採用しても receiver で frontmatter (`RenderMarkdown` 出力) に詰め直す必要があり、wire format の標準化と app-level schema の標準化が分離してしまう
7. **sdk-go pubsub/v2 module は Go 1.25.7 必須**。本リポは Go 1.26 なので OK だが、CI の Go version 固定に注意

---

## Conclusion

**不採用 (現状維持)**。本判断と再検討トリガー条件は ADR 0022 で記録する。

**採用条件 (将来再検討すべきトリガー)**:

- 5本柱外の publisher (例: GitHub Actions / 別 Cloud Run service) が `dmail-inbound` topic に publish する具体プランが intent.md に追加されたとき
- exe-coder VM の dmail-receiver を Cloud Run HTTP service 化する大型リファクタが計画され、Eventarc trigger に乗せ替える話が出たとき
- 5本柱本体が Markdown frontmatter ではなく構造化 event を直接読むよう改修される計画が出たとき

---

## 参照した公式 URL リスト

- https://cloudevents.io/ — プロジェクト公式トップ (CNCF Graduated 2024-01-25, SQL V1 2024-06-13)
- https://github.com/cloudevents/spec — spec repo
- https://github.com/cloudevents/spec/releases/tag/v1.0.2 — 最新 stable v1.0.2 (2024-02-06)
- https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md — context attribute spec
- https://github.com/cloudevents/spec/blob/main/cloudevents/extensions/distributed-tracing.md — distributed tracing extension
- https://github.com/cloudevents/spec/blob/main/cloudevents/bindings/http-protocol-binding.md — HTTP binding
- https://github.com/cloudevents/sdk-go — Go SDK repo (v2.16.2)
- https://pkg.go.dev/github.com/cloudevents/sdk-go/protocol/pubsub/v2 — Pub/Sub protocol (cloud.google.com/go/pubsub/v2 依存)
- https://pkg.go.dev/github.com/cloudevents/sdk-go/observability/opentelemetry/v2 — OTel observability module
- https://github.com/cloudevents/sdk-go/blob/main/protocol/pubsub/v2/protocol.go — Pub/Sub binding 実装
- https://github.com/cloudevents/sdk-go/blob/main/protocol/pubsub/v2/options.go — `WithClient` 等 option 一覧
- https://github.com/googleapis/google-cloudevents/blob/main/docs/spec/pubsub.md — Google 公式 Pub/Sub binding spec ("working draft" 表記)
- https://docs.cloud.google.com/eventarc/docs/cloudevents — Eventarc CloudEvents format (last updated 2026-05-01)
- https://docs.cloud.google.com/eventarc/advanced/docs/publish-events/publish-events-direct-format — Eventarc direct publish
- https://docs.cloud.google.com/pubsub/docs/open-telemetry-tracing — `EnableOpenTelemetryTracing` 公式 (last updated 2026-05-01) — ADR 0021 の根拠
- https://www.w3.org/TR/trace-context/ — W3C Trace Context spec
- https://opentelemetry.io/docs/specs/semconv/messaging/gcp-pubsub/ — Pub/Sub messaging semconv

## 参照した本リポ ファイル

- `internal/adapter/output/pubsub/publisher.go` — 自前 attribute 構築箇所 (l.102-126)
- `internal/adapter/input/pubsub/receiver.go` — message 受信 / id attribute 抽出
- `internal/core/domain/dmail.go` — RenderMarkdown / ParseDMail (5本柱境界の serialization)
- `docs/adr/0013-pubsub-bridge-for-outbox.md` — Pub/Sub bridge schema
- `docs/adr/0021-pubsub-trace-context-library-managed.md` — `EnableOpenTelemetryTracing` 委譲、CloudEvents 採用判断と直接衝突する決定
