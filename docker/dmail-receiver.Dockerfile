# dmail-receiver — pulls D-Mail messages from Pub/Sub `dmail-inbound-receiver`
# and atomic-writes <id>.md into the phonewave outbox dir on the workspace VM.
#
# Deployment: a host-OS systemd unit on the workspace VM execs
#   docker run --rm --name dmail-receiver --network host \
#     -v /var/lib/phonewave/outbox:/outbox <this-image>
# wired up by exe/coder/templates/dotfiles-devcontainer/main.tf in the
# dotfiles repo (see runops-gateway experiments/2026-05-06_dotfiles-
# dmail-daemon-placement.md).
#
# Multi-stage with the same base + flags as the root Dockerfile so all
# three runops-gateway binaries share an image discipline (CGO_ENABLED=0,
# distroless static, runs as non-root via the `nonroot` UID 65532
# baked into gcr.io/distroless/static-debian12:nonroot).

FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /dmail-receiver ./cmd/dmail-receiver

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /dmail-receiver /dmail-receiver

USER nonroot:nonroot

ENTRYPOINT ["/dmail-receiver"]
