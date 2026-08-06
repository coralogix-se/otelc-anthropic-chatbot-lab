# otelc Anthropic Chatbot Lab

Small Go chatbot lab for [OpenTelemetry Go compile-time instrumentation (`otelc`)](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation).

The application source has **no OpenTelemetry imports or SDK setup**. Building with `otelc go build` injects instrumentation for:

| Library | What you'll see in Coralogix |
| --- | --- |
| `net/http` | Server request spans / metrics for `/api/chat`, `/healthz`, UI |
| `github.com/anthropics/anthropic-sdk-go` | GenAI spans for Claude `Messages.New` |
| `github.com/redis/go-redis/v9` | Redis command spans (cache + session activity) |
| `database/sql` (+ SQLite) | DB spans for chat history persistence |
| `log/slog` | Structured logs correlated with traces |

Telemetry is exported over OTLP/HTTP to Coralogix.

## Architecture

```text
Browser UI ──► Go net/http chatbot
                 ├─► Anthropic Messages API
                 ├─► Redis (reply cache / session activity)
                 └─► SQLite (conversation history)
                        │
                        ▼  (injected by otelc at build time)
                   OTLP/HTTP ──► Coralogix (us2 ingress)
```

## Prerequisites

- Go 1.25+
- Docker (for Redis)
- [`otelc`](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation) with **Anthropic instrumentation** (present on `main`; not in the `v1.0.1` release bundle yet):

```bash
./scripts/install-otelc.sh
# or: go install go.opentelemetry.io/otelc/tool/cmd/otelc@latest  # HTTP/Redis/SQL/slog only on v1.0.1
```
- Anthropic API key
- Coralogix Send-Your-Data API key

## Quick start

```bash
cp .env.example .env
# edit .env — set ANTHROPIC_API_KEY and CORALOGIX_API_KEY

make docker-up          # Redis on :6379
make build              # otelc go build
./scripts/run-with-coralogix.sh
```

In another terminal:

```bash
./scripts/smoke.sh
# or open http://127.0.0.1:8080
```

### Docker (optional)

```bash
docker compose --profile app up --build
```

## Coralogix settings

This lab targets **US2**:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=https://ingress.us2.coralogix.com:443
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer <CORALOGIX_API_KEY>
OTEL_RESOURCE_ATTRIBUTES=cx.application.name=otelc-lab,cx.subsystem.name=anthropic-chatbot
OTEL_SERVICE_NAME=otelc-anthropic-chatbot
```

In Explore / Tracing, filter on:

- `applicationName:otelc-lab` / `subsystemName:anthropic-chatbot`
- `service.name:"otelc-anthropic-chatbot"`
- GenAI / Anthropic spans from chat turns
- Child Redis + SQLite spans under the `/api/chat` server span

## Build modes

```bash
make build        # instrumented binary via otelc
make build-plain  # same source, no instrumentation (comparison)
```

## API

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/` | Chat UI |
| `GET` | `/healthz` | Health (Redis + SQLite) |
| `POST` | `/api/chat` | `{ "session_id"?: "...", "message": "..." }` |
| `GET` | `/api/sessions/{id}` | Conversation history |

## Why this demonstrates otelc well

1. **Zero code changes for telemetry** — search the Go sources; there is no `go.opentelemetry.io/otel` usage.
2. **Third-party GenAI coverage** — Anthropic SDK calls become GenAI spans without wrappers.
3. **Multi-dependency traces** — one user chat fans out into HTTP + LLM + Redis + SQL.
4. **Drop-in build swap** — CI/local builds use `otelc go build` instead of `go build`.

## References

- [otelc getting started](https://opentelemetry.io/docs/zero-code/go/compile-time/getting-started/)
- [Supported libraries](https://opentelemetry.io/docs/zero-code/go/compile-time/supported-libraries/)
- [Coralogix OpenTelemetry](https://coralogix.com/docs/opentelemetry/getting-started/)

## License

Apache-2.0

## Lab notes

- Anthropic GenAI compile-time rules are on `otelc` **main** (not in the `v1.0.1` release bundle). Use `./scripts/install-otelc.sh`.
- This lab was validated against Coralogix **US2** ingress with a Send-Your-Data key (`cxtp_…`).
- Do **not** commit `.env`. Keep `CORALOGIX_API_KEY` and `ANTHROPIC_API_KEY` local only.
