# otelc Anthropic chatbot lab

MODULE := github.com/coralogix-se/otelc-anthropic-chatbot-lab
BINARY := bin/chatbot
GOBIN  := $(shell go env GOPATH)/bin
export PATH := $(GOBIN):$(PATH)
OTELC  ?= otelc

.PHONY: deps build build-plain run docker-up docker-down smoke clean tidy

deps:
	go mod tidy
	@command -v $(OTELC) >/dev/null || ./scripts/install-otelc.sh

## Build with compile-time OpenTelemetry instrumentation
build: deps
	mkdir -p bin
	$(OTELC) go build -o $(BINARY) ./cmd/chatbot

## Reference build without instrumentation
build-plain: deps
	mkdir -p bin
	go build -o $(BINARY)-plain ./cmd/chatbot

run: build
	@test -n "$$ANTHROPIC_API_KEY" || (echo "ANTHROPIC_API_KEY is required" && exit 1)
	@test -n "$$CORALOGIX_API_KEY" || (echo "CORALOGIX_API_KEY is required" && exit 1)
	set -a && [ -f .env ] && . ./.env; set +a; \
	OTEL_SERVICE_NAME=$${OTEL_SERVICE_NAME:-otelc-anthropic-chatbot} \
	OTEL_EXPORTER_OTLP_ENDPOINT=$${OTEL_EXPORTER_OTLP_ENDPOINT:-https://ingress.us2.coralogix.com:443} \
	OTEL_EXPORTER_OTLP_PROTOCOL=$${OTEL_EXPORTER_OTLP_PROTOCOL:-http/protobuf} \
	OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer $${CORALOGIX_API_KEY}" \
	OTEL_RESOURCE_ATTRIBUTES=$${OTEL_RESOURCE_ATTRIBUTES:-cx.application.name=otelc-lab,cx.subsystem.name=anthropic-chatbot,service.version=0.1.0} \
	REDIS_ADDR=$${REDIS_ADDR:-localhost:6379} \
	./$(BINARY) -addr $${LISTEN_ADDR:-:8080}

docker-up:
	docker compose up -d redis

docker-down:
	docker compose down

smoke:
	@./scripts/smoke.sh

tidy:
	go mod tidy

clean:
	rm -rf bin .otelc-build otelc.runtime.go otel.instrumentation.go data
