FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /runops-gateway ./cmd/server

FROM gcr.io/distroless/static-debian12

COPY --from=builder /runops-gateway /runops-gateway

EXPOSE 8080

ENTRYPOINT ["/runops-gateway"]
