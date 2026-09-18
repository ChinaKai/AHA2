#!/usr/bin/env bash
# Regenerates the Windows icon resources from assets/aha2.ico.
#
# The two .syso files are committed because they are what the Go linker picks up
# automatically; regenerating them requires the rsrc tool, which needs network
# access a build machine may not have. Run this only after changing the mark in
# assets/make-logo.py.
set -Eeuo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
ico="$repo_dir/assets/aha2.ico"
rsrc="${RSRC:-rsrc}"

[[ -f "$ico" ]] || { printf 'Missing icon source: %s\n' "$ico" >&2; exit 1; }
command -v "$rsrc" >/dev/null 2>&1 || {
  printf 'rsrc was not found. Install it with:\n  go install github.com/akavel/rsrc@latest\n' >&2
  exit 1
}

# Both architectures the release builds. Go selects a .syso by its filename
# suffix, so an amd64 file is ignored on arm64 — each needs its own.
for arch in amd64 arm64; do
  for dir in cmd/aha cmd/aha-tray; do
    output="$repo_dir/$dir/rsrc_windows_$arch.syso"
    "$rsrc" -arch "$arch" -ico "$ico" -o "$output"
    printf 'wrote %s\n' "${output#"$repo_dir"/}"
  done
done
