#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_bin="${GO_BIN:-$repo/.tools/go/bin/go}"
mkdir -p "$repo/dist"

if [[ -d "$repo/web/dist" ]]; then
  mkdir -p "$repo/internal/webassets/dist"
  find "$repo/internal/webassets/dist" -mindepth 1 -maxdepth 1 -type f -delete
  cp -R "$repo/web/dist/." "$repo/internal/webassets/dist/"
fi

cd "$repo"
"$go_bin" test ./...
for target in linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64; do
  os="${target%/*}"
  arch="${target#*/}"
  suffix=""
  [[ "$os" == "windows" ]] && suffix=".exe"
  echo "Building $os/$arch"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "$go_bin" build -trimpath -ldflags="-s -w" -o "dist/aha2-${os}-${arch}${suffix}" ./cmd/aha
done
