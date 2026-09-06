#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
build="$repo/scripts/build-macos-packages.sh"

bash -n "$build" "$repo/packaging/macos/scripts/preinstall" \
  "$repo/packaging/macos/scripts/postinstall" "$repo/packaging/macos/uninstall-aha2.sh"

missing="${TMPDIR:-/tmp}/aha2-validation-$RANDOM-$RANDOM"
bash "$build" --version v1.2.3 --input-exe "$missing-amd64" --arch amd64 --output-dir "$missing-output" --validate-only
bash "$build" --version v1.2.3 --input-exe "$missing-arm64" --arch arm64 --output-dir "$missing-output" --validate-only

python3 - "$repo/packaging/macos/com.aha2.controlplane.plist" <<'PY'
import plistlib
import sys

with open(sys.argv[1], "rb") as source:
    plist = plistlib.load(source)
assert plist["Label"] == "com.aha2.controlplane"
assert plist["ProgramArguments"] == [
    "/usr/local/bin/aha2", "serve", "--listen", "127.0.0.1:8766",
    "--data-dir", "/Library/Application Support/AHA2",
]
assert plist["RunAtLoad"] is True
assert plist["KeepAlive"] is True
assert plist["StandardOutPath"].endswith("/aha2.log")
assert plist["StandardErrorPath"].endswith("/aha2-error.log")
PY

grep -Fq 'AHA2-macos-${arch}.pkg' "$build"
grep -Fq -- '--sign "$signing_identity"' "$build"
grep -Fq 'launchctl bootout' "$repo/packaging/macos/uninstall-aha2.sh"
grep -Fq 'Persistent data was retained' "$repo/packaging/macos/uninstall-aha2.sh"
if grep -Eq 'rm .*Library/Application Support/AHA2' "$repo/packaging/macos/uninstall-aha2.sh"; then
  echo "Uninstaller must retain persistent data." >&2
  exit 1
fi

echo "macOS package contracts are valid."
