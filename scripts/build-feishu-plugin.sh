#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# GO_BIN may be an absolute path or a bare command name (release CI passes
# GO_BIN=go); resolve bare names through PATH.
if [[ -n "${GO_BIN:-}" ]]; then
  if [[ "$GO_BIN" == */* ]]; then
    go_bin="$GO_BIN"
  else
    go_bin="$(command -v "$GO_BIN" 2>/dev/null || true)"
  fi
elif [[ -x "$repo/.tools/go/bin/go" ]]; then
  go_bin="$repo/.tools/go/bin/go"
else
  go_bin="$(command -v go || true)"
fi
[[ -n "$go_bin" && -x "$go_bin" ]] || { echo "Go toolchain was not found: GO_BIN=${GO_BIN:-}" >&2; exit 1; }
target="${1:-windows/amd64}"
os="${target%/*}"
arch="${target#*/}"
suffix=""
[[ "$os" == "windows" ]] && suffix=".exe"
# AHA2_PLUGIN_OUTPUT_DIR lets a package build place the plugin inside its own
# output directory instead of the repository dist/ tree. Resolve it against the
# caller's working directory before the build changes into the plugin module.
output_dir="$(realpath -m "${AHA2_PLUGIN_OUTPUT_DIR:-$repo/dist/plugins/feishu/$os-$arch}")"
output="$output_dir/aha2-channel-feishu$suffix"

mkdir -p "$output_dir"
(
  cd "$repo/plugins/feishu"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "$go_bin" build -buildvcs=false -trimpath -ldflags="-s -w" -o "$output" .
)
cp "$repo/plugins/feishu/plugin.json" "$output_dir/plugin.json"
digest="$(sha256sum "$output" | cut -d' ' -f1)"
sed -i "s/SET_BY_PACKAGING/$digest/" "$output_dir/plugin.json"
echo "Built $output"
