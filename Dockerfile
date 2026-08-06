# Build with otelc compile-time instrumentation (main tip includes Anthropic),
# then run a slim runtime image.
FROM golang:1.26-bookworm AS builder

WORKDIR /src
RUN apt-get update && apt-get install -y --no-install-recommends git make   && rm -rf /var/lib/apt/lists/*   && git clone --depth 1 https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation.git /tmp/otelc   && make -C /tmp/otelc build   && install -m 755 /tmp/otelc/otelc /usr/local/bin/otelc   && otelc version

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN mkdir -p bin && otelc go build -o /out/chatbot ./cmd/chatbot

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates   && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /out/chatbot /app/chatbot
COPY web /app/web

ENV LISTEN_ADDR=:8080 \
    DB_PATH=/data/chatbot.db \
    STATIC_DIR=/app/web \
    REDIS_ADDR=redis:6379

EXPOSE 8080
ENTRYPOINT ["/app/chatbot"]
