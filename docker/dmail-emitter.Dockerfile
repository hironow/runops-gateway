# dmail-emitter — fsnotify-watches the 5pillar archive directory bind-mounted
# at /archive (= host /var/lib/phonewave/archive) and publishes any new
# D-Mail .md files to the Pub/Sub `dmail-outbound` topic so the gateway
# can fan results into Slack threads.
#
# Deployment: a host-OS systemd unit on the workspace VM execs
#   docker run --rm --name dmail-emitter --network host \
#     -v /var/lib/phonewave/archive:/archive:ro <this-image>
# wired up by exe/coder/templates/dotfiles-devcontainer/main.tf in the
# dotfiles repo (see runops-gateway experiments/2026-05-06_dotfiles-
# dmail-daemon-placement.md).
#
# Same multi-stage / distroless-nonroot recipe as docker/dmail-receiver
# .Dockerfile so all runops-gateway daemons share an image discipline.

FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /dmail-emitter ./cmd/dmail-emitter

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /dmail-emitter /dmail-emitter

USER nonroot:nonroot

ENTRYPOINT ["/dmail-emitter"]
