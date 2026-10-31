#!/bin/bash
RUN=40653201413
LOG=/home/kaikai/kk-workspace/my_project/AHA2/tmp/release-05149-watch.log
: > "$LOG"
for i in $(seq 1 160); do
  json=$(curl -sS --max-time 25 --retry 2 --retry-delay 2 --retry-all-errors "https://api.github.com/repos/ChinaKai/AHA2/actions/runs/$RUN" 2>/dev/null)
  line=$(printf '%s' "$json" | python3 -c "
import sys,json
try:
    r=json.load(sys.stdin)
    print(r.get('status'),r.get('conclusion'))
except Exception:
    print('parse_error','')
" 2>/dev/null)
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $line" | tee -a "$LOG"
  case "$line" in
    completed*) break;;
  esac
  sleep 30
done
echo "=== JOBS ===" | tee -a "$LOG"
curl -sS --max-time 30 --retry 3 --retry-delay 2 --retry-all-errors "https://api.github.com/repos/ChinaKai/AHA2/actions/runs/$RUN/jobs?per_page=20" 2>/dev/null | python3 -c "
import sys,json
d=json.load(sys.stdin)
for j in d.get('jobs',[]):
    print(j.get('name'),'|',j.get('status'),j.get('conclusion'))
" | tee -a "$LOG"
echo "WATCH_DONE"
