#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_bin="${GO_BIN:-$repo/.tools/go/bin/go}"
target="${1:-windows/amd64}"
os="${target%/*}"
arch="${target#*/}"
suffix=""
[[ "$os" == "windows" ]] && suffix=".exe"
output_dir="$repo/dist/plugins/feishu/$os-$arch"
output="$output_dir/aha2-channel-feishu$suffix"

mkdir -p "$output_dir"
(
  cd "$repo/plugins/feishu"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "$go_bin" build -trimpath -ldflags="-s -w" -o "$output" .
)
cp "$repo/plugins/feishu/plugin.json" "$output_dir/plugin.json"
digest="$(sha256sum "$output" | cut -d' ' -f1)"
sed -i "s/SET_BY_PACKAGING/$digest/" "$output_dir/plugin.json"
echo "Built $output"
