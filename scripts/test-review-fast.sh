#!/usr/bin/env bash
# 评审快速测试档：用 5 分钟超时预算跑 app/httpapi/store 三个高频包。
# 只跳过 -short 清单里的重集成/长等待用例；默认 go test 覆盖不变。
# 用法与跳过清单见 docs/testing.md。
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_bin="${GO_BIN:-go}"
test_timeout="${TEST_TIMEOUT:-300s}"

packages=(./internal/app ./internal/httpapi ./internal/store)

cd "$repo"
printf 'fast test:'
printf ' %q' "$go_bin" test -short "${packages[@]}" -count=1 -timeout "$test_timeout" "$@"
printf '\n'

started="$(date +%s)"
set +e
"$go_bin" test -short "${packages[@]}" -count=1 -timeout "$test_timeout" "$@"
status=$?
set -e
elapsed=$(( $(date +%s) - started ))

if [[ $status -eq 0 ]]; then
  echo "fast test passed in ${elapsed}s"
else
  echo "fast test failed in ${elapsed}s (exit ${status})" >&2
fi
exit "$status"
