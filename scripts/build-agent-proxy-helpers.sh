#!/usr/bin/env bash
# Build the Agent API stdio helpers that ship next to the aha2 executable.
#
# The helper is the same aha2 binary as the hidden `agent-proxy` subcommand. A
# Windows AHA cannot upload its own aha2.exe to a Linux workspace, so the
# release package carries a cross-compiled helper per workspace platform. The
# runtime reads manifest.json here and verifies os/arch/sha256 before using one.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_bin="${GO_BIN:-$repo/.tools/go/bin/go}"
output="${1:-$repo/dist/agent-proxy}"
targets="${AHA2_AGENT_PROXY_TARGETS:-linux/amd64 linux/arm64}"
base_version="${VERSION:-$(git -C "$repo" describe --tags --abbrev=0 2>/dev/null || printf 'v0.0.0')}"
base_version="${base_version#v}"
if [[ ! "$base_version" =~ ^[0-9]+([.][0-9]+){2}$ ]]; then
  echo "Version must use x.y.z format: $base_version" >&2
  exit 2
fi
web_version="${WEB_VERSION:-$(bash "$repo/scripts/web-version.sh" "$base_version")}"

mkdir -p "$output"
cd "$repo"

entries=""
for target in $targets; do
  os="${target%/*}"
  arch="${target#*/}"
  if [[ "$os" == "windows" ]]; then
    echo "Windows workspaces do not use the stdio proxy: $target" >&2
    exit 2
  fi
  name="aha2-agent-proxy-${os}-${arch}"
  echo "Building agent-proxy helper $os/$arch"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "$go_bin" build -trimpath \
    -ldflags="-s -w -X main.version=$base_version -X main.webVersion=$web_version" \
    -o "$output/$name" ./cmd/aha
  digest="$(sha256sum "$output/$name" | cut -d' ' -f1)"
  entry="{\"os\":\"$os\",\"arch\":\"$arch\",\"file\":\"$name\",\"sha256\":\"$digest\"}"
  if [[ -n "$entries" ]]; then
    entries="$entries,$entry"
  else
    entries="$entry"
  fi
done

cat > "$output/manifest.json" <<EOF
{"protocol":1,"version":"$base_version","helpers":[$entries]}
EOF

echo "Agent proxy helpers: $output (version $base_version)"
