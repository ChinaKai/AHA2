#!/usr/bin/env bash
# End-to-end harness for the real DSH live-inbox plugin against a mock AHA API.
# Modes: tool (default), turn-stopping, disabled.
set -u
ROOT="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$ROOT/../.." && pwd)"
MODE="${MODE:-tool}"
MODEL_PORT=18768
AHA_PORT=18767
rm -rf "$ROOT/home"
mkdir -p "$ROOT/home/profiles"
cp -a "$HOME/.dsh/profiles/headless" "$ROOT/home/profiles/"
rm -f "$ROOT/aha-requests.jsonl" "$ROOT/model-requests.jsonl" "$ROOT/dsh.stdout.jsonl" "$ROOT/dsh.stderr.log"

MODEL_MODE=tool
LIVE_ENABLED=1
if [ "$MODE" = "turn-stopping" ]; then MODEL_MODE=final; fi
if [ "$MODE" = "disabled" ]; then LIVE_ENABLED=0; fi

cat >"$ROOT/patch.yml" <<YAML
- id: llm-pi-ai
  config:
    providers:
      mock:
        displayName: Live Inbox Mock
        api: openai-completions
        baseURL: "http://127.0.0.1:$MODEL_PORT/v1"
        apiKeyEnv: MOCK_API_KEY
        models:
          - id: "mock-model"
            contextWindow: 100000
            reasoningEfforts:
              off: off
              low: low
              medium: medium
              high: high
- id: agent-default-model
  config:
    provider: mock
    model: "mock-model"
- id: session-title-llm
  disabled: true
- id: approval
  config:
    policy: never
- insert:
    - id: aha-live-inbox
      name: "$REPO/internal/backend/aha_live_inbox.mjs"
YAML

MODEL_MODE="$MODEL_MODE" python3 "$ROOT/mock_model.py" "$MODEL_PORT" >"$ROOT/model.log" 2>&1 &
MODEL_PID=$!
python3 "$ROOT/mock_aha.py" "$AHA_PORT" >"$ROOT/aha.log" 2>&1 &
AHA_PID=$!
trap 'kill $MODEL_PID $AHA_PID 2>/dev/null' EXIT
for _ in $(seq 1 50); do
  if grep -q listening "$ROOT/model.log" 2>/dev/null && grep -q listening "$ROOT/aha.log" 2>/dev/null; then break; fi
  sleep 0.1
done

DSH_HOME="$ROOT/home" \
MOCK_API_KEY="spike-not-a-secret" \
AHA2_AGENT_API_URL="http://127.0.0.1:$AHA_PORT" \
AHA2_AGENT_API_TOKEN="spike-token" \
AHA2_LIVE_INBOX="$LIVE_ENABLED" \
AHA2_LIVE_INBOX_DEBUG=1 \
DSH_PERMISSION_MODE=danger-full-access \
dsh --profile headless --patch "$ROOT/patch.yml" --json - \
  >"$ROOT/dsh.stdout.jsonl" 2>"$ROOT/dsh.stderr.log" <<'PROMPT'
Run the harness command once, then report the result.
PROMPT
STATUS=$?
echo "mode=$MODE dsh_exit=$STATUS"
echo "--- dsh final ---"
grep -E '"type":"(final|text|tool_call|tool_result)"' "$ROOT/dsh.stdout.jsonl" | tail -10
echo "--- AHA mock requests ---"
cat "$ROOT/aha-requests.jsonl" 2>/dev/null | python3 -c 'import json,sys
for line in sys.stdin:
    row=json.loads(line); print(row["path"], json.dumps(row["body"], ensure_ascii=False))'
echo "--- model requests ---"
python3 - "$ROOT/model-requests.jsonl" <<'PY'
import json, sys
try:
    rows=[json.loads(line) for line in open(sys.argv[1], encoding='utf-8')]
except FileNotFoundError:
    print('no model requests'); raise SystemExit
for i,row in enumerate(rows,1):
    print(i, 'roles=', row['roles'], 'marker=', row['marker'])
    if row['marker']:
        for message in row['messages']:
            content=message.get('content')
            if isinstance(content,str) and 'AHA live inbox' in content:
                print('   injected=', content[:280].replace('\n',' | '))
PY
echo "--- plugin debug ---"
grep 'aha-live-inbox' "$ROOT/dsh.stderr.log" | tail -5
exit $STATUS
