#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
PROMPT="${PROMPT:-In one short sentence, what is OpenTelemetry compile-time instrumentation for Go?}"

if ! curl -fsS --max-time 1 "$BASE_URL/healthz" >/dev/null 2>&1; then
	if [ "$BASE_URL" = "http://127.0.0.1:8080" ] && curl -fsS --max-time 1 "http://[::1]:8080/healthz" >/dev/null 2>&1; then
		BASE_URL="http://[::1]:8080"
	fi
fi

echo "==> health"
curl -fsS "$BASE_URL/healthz" | tee /tmp/otelc-lab-health.json
echo

echo "==> chat"
RESP=$(curl -fsS -X POST "$BASE_URL/api/chat" \
  -H 'Content-Type: application/json' \
  -d "$(python3 - <<PY
import json
print(json.dumps({"message": """$PROMPT"""}))
PY
)")
echo "$RESP" | tee /tmp/otelc-lab-chat.json
echo

SESSION=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["session_id"])' <<<"$RESP")

echo "==> replay (should hit redis cache)"
curl -fsS -X POST "$BASE_URL/api/chat" \
  -H 'Content-Type: application/json' \
  -d "$(python3 - <<PY
import json
print(json.dumps({"session_id": "$SESSION", "message": """$PROMPT"""}))
PY
)" | tee /tmp/otelc-lab-chat-cached.json
echo

echo "==> session history"
curl -fsS "$BASE_URL/api/sessions/$SESSION" | tee /tmp/otelc-lab-session.json
echo
echo "OK — session $SESSION"
