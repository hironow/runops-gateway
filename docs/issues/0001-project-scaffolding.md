# Issue 0001: Project Scaffolding

## Goal

Go プロジェクトの骨格を作成する。

## Tasks

- [x] `go.mod` 初期化（module: `github.com/hironow/runops-gateway`）
- [x] ディレクトリ構造の作成（cmd/, internal/, opentofu/, tests/）
- [x] `justfile` の作成（build, run, test, lint, fmt, tidy, check）
- [x] `Dockerfile` の作成（multi-stage build: golang:1.26-alpine + distroless）
- [x] `.gitignore` の整備

## Definition of Done (DoD)

- [x] `just build` でバイナリ（`cmd/server`, `cmd/runops`）がビルドできる
- [x] `just test` でテストが実行できる（空でも可）
- [x] `just lint` で `go vet` が通る
- [ ] `just fmt` で `gofmt` による差分が出ない
- [ ] Dockerfile が `docker build` でエラーなくビルドできる（`go.sum` 整備後）

## 非機能要件

- **セキュリティ**: Dockerfile は distroless ベースイメージを使用し、シェルを含まない
- **再現性**: `go.mod` + `go.sum` で依存関係が完全に固定されること
- **ポータビリティ**: `CGO_ENABLED=0 GOOS=linux` でクロスコンパイル可能なこと

## Status: Done
