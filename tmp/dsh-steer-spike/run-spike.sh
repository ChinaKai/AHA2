#!/usr/bin/env bash
# Reproducible DSH steer spike: isolated DSH_HOME + deterministic mock model.
set -u
ROOT="$(cd "$(dirname "$0")" && pwd)"
PORT=18766
rm -f "$ROOT/requests.jsonl" "$ROOT/steer-events.jsonl" "$ROOT/dsh.stdout.jsonl" "$ROOT/dsh.stderr.log" "$ROOT/mock.log"

python3 "$ROOT/mock_provider.py" "$PORT" >"$ROOT/mock.log" 2>&1 &
MOCK_PID=$!
trap 'kill $MOCK_PID 2>/dev/null' EXIT
for _ in $(seq 1 50); do
  if grep -q "listening" "$ROOT/mock.log" 2>/dev/null; then break; fi
  sleep 0.1
done

DSH_HOME="$ROOT/home" \
MOCK_API_KEY="spike-not-a-secret" \
DSH_PERMISSION_MODE=danger-full-access \
SPIKE_STEER_LOG="$ROOT/steer-events.jsonl" \
SPIKE_DEFER="${SPIKE_DEFER:-microtask}" \
SPIKE_TRIGGER="${SPIKE_TRIGGER:-tool-result}" \
dsh --profile headless --patch "$ROOT/spike-patch.yml" --json - \
  >"$ROOT/dsh.stdout.jsonl" 2>"$ROOT/dsh.stderr.log" <<'PROMPT'
Use the bash tool exactly once to run: echo SPIKE-TOOL-RAN
Then report the command output.
PROMPT
STATUS=$?
echo "dsh exit=$STATUS"
echo "--- stdout ---"
cat "$ROOT/dsh.stdout.jsonl"
echo "--- stderr (tail) ---"
tail -30 "$ROOT/dsh.stderr.log"
echo "--- steer events ---"
cat "$ROOT/steer-events.jsonl" 2>/dev/null
echo "--- provider requests ---"
python3 - "$ROOT/requests.jsonl" <<'PY'
import json, sys
try:
    rows = [json.loads(line) for line in open(sys.argv[1], encoding='utf-8')]
except FileNotFoundError:
    print('no provider requests recorded')
    raise SystemExit(0)
print(f'{len(rows)} request(s)')
for i, row in enumerate(rows, 1):
    print(i, 'roles=', row['roles'], 'has_marker=', row['has_marker'])
    print('   tail messages=', json.dumps(row['messages'][-3:], ensure_ascii=False)[:1200])
PY
exit $STATUS
