#!/bin/bash
RUN=40433828100
for i in $(seq 1 80); do
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
try:
    d=json.load(sys.stdin)
    for j in d.get('jobs',[]):
        print(j.get('name'),'|',j.get('status'),j.get('conclusion'))
except Exception as e:
    print('jobs_parse_error',e)
"
echo "=== RELEASE ==="
curl -sS "https://api.github.com/repos/ChinaKai/AHA2/releases/tags/v0.5.147" 2>/dev/null | python3 -c "
import sys,json
try:
    d=json.load(sys.stdin)
    print('id=',d.get('id'),'draft=',d.get('draft'),'name=',d.get('name'))
    for a in d.get('assets',[]): print('asset',a.get('name'),a.get('size'))
except Exception as e:
    print('release_parse_error',e)
"
