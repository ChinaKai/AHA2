#!/bin/bash
RUN=40455737421
for i in $(seq 1 120); do
  json=$(curl -sS "https://api.github.com/repos/ChinaKai/AHA2/actions/runs/$RUN" 2>/dev/null)
  line=$(printf '%s' "$json" | python3 -c "
import sys,json
try:
    r=json.load(sys.stdin)
    print(r.get('status'),r.get('conclusion'))
except Exception:
    print('parse_error','')
" 2>/dev/null)
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $line"
  case "$line" in
    completed*) break;;
  esac
  sleep 20
done
echo "=== JOBS ==="
curl -sS "https://api.github.com/repos/ChinaKai/AHA2/actions/runs/$RUN/jobs?per_page=20" 2>/dev/null | python3 -c "
import sys,json
d=json.load(sys.stdin)
for j in d.get('jobs',[]):
    print(j.get('name'),'|',j.get('status'),j.get('conclusion'))
"
