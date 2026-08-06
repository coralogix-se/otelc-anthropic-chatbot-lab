#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ -f .env ]]; then
  # shellcheck disable=SC1091
  set -a && source .env && set +a
fi

: "${ANTHROPIC_API_KEY:?ANTHROPIC_API_KEY is required}"
: "${CORALOGIX_API_KEY:?CORALOGIX_API_KEY is required}"

export OTEL_SERVICE_NAME="${OTEL_SERVICE_NAME:-otelc-anthropic-chatbot}"
export OTEL_EXPORTER_OTLP_ENDPOINT="${OTEL_EXPORTER_OTLP_ENDPOINT:-https://ingress.us2.coralogix.com:443}"
export OTEL_EXPORTER_OTLP_PROTOCOL="${OTEL_EXPORTER_OTLP_PROTOCOL:-http/protobuf}"
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer ${CORALOGIX_API_KEY}"
export OTEL_RESOURCE_ATTRIBUTES="${OTEL_RESOURCE_ATTRIBUTES:-cx.application.name=otelc-lab,cx.subsystem.name=anthropic-chatbot,service.version=0.1.0}"
export REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"
export DB_PATH="${DB_PATH:-./data/chatbot.db}"
export STATIC_DIR="${STATIC_DIR:-./web}"
export LISTEN_ADDR="${LISTEN_ADDR:-:8080}"

make build
exec ./bin/chatbot -addr "$LISTEN_ADDR"
