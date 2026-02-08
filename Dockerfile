FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /mautrix-wechat \
    ./cmd/mautrix-wechat

# Runtime image — minimal attack surface
FROM alpine:3.19

RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    ffmpeg \
    curl \
    && rm -rf /var/cache/apk/*

COPY --from=builder /mautrix-wechat /usr/local/bin/mautrix-wechat

# Non-root user
RUN addgroup -S mautrix && adduser -S -G mautrix -u 1337 -H mautrix

WORKDIR /app
RUN mkdir -p /app/config /app/data /app/logs && chown -R mautrix:mautrix /app

USER mautrix

VOLUME ["/app/config", "/app/data", "/app/logs"]

EXPOSE 29350 29352 9110

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -sf http://localhost:9110/health || exit 1

ENTRYPOINT ["mautrix-wechat"]
CMD ["-config", "/app/config/config.yaml"]
