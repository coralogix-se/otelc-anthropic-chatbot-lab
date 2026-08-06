#!/usr/bin/env bash
# Anthropic GenAI instrumentation landed after otelc v1.0.1.
# Install a tip build that includes github.com/anthropics/anthropic-sdk-go rules.
set -euo pipefail
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
git clone --depth 1 https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation.git "$ROOT/otelc"
make -C "$ROOT/otelc" build
install -m 755 "$ROOT/otelc/otelc" "${GOPATH:-$HOME/go}/bin/otelc"
"${GOPATH:-$HOME/go}/bin/otelc" version
