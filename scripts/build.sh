#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_bin="${GO_BIN:-$repo/.tools/go/bin/go}"
output="${1:-$repo/dist/aha2}"

mkdir -p "$repo/internal/webassets/dist" "$(dirname "$output")"
if [[ -d "$repo/web/dist" ]]; then
  find "$repo/internal/webassets/dist" -mindepth 1 -maxdepth 1 -type f -delete
  cp -R "$repo/web/dist/." "$repo/internal/webassets/dist/"
fi

cd "$repo"
"$go_bin" test ./...
CGO_ENABLED=0 "$go_bin" build -trimpath -ldflags="-s -w" -o "$output" ./cmd/aha
echo "Built $output"
