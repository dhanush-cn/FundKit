# Engineered by Dhanush C N (github.com/dhanush-cn)
FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# tidy is a safety net: it repairs go.sum if a checksum is missing, so a fresh
# clone builds without the developer needing a local Go toolchain.
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/portfolio-service ./cmd/server

FROM alpine:3.20
RUN apk --no-cache add ca-certificates wget && adduser -D -u 10001 fundkit
WORKDIR /app
COPY --from=builder /out/portfolio-service .
USER fundkit
EXPOSE 50051 8082
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8082/healthz || exit 1
ENTRYPOINT ["./portfolio-service"]
