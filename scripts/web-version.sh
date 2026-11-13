#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
base_version="${1:-}"
base_version="${base_version#v}"

if [[ ! "$base_version" =~ ^[0-9]+([.][0-9]+){2}$ ]]; then
  echo "Base version must use x.y.z format." >&2
  exit 2
fi

build_date="${AHA2_BUILD_DATE:-$(date -u +%Y%m%d)}"
# AHA2_GIT_HASH lets a build outside a git checkout (for example a source
# snapshot) name the commit it was produced from. It also means the caller has
# already decided which commit this is, so the working-tree dirty probe is
# skipped instead of failing on a missing .git directory.
explicit_hash="${AHA2_GIT_HASH:-}"
git_hash="$explicit_hash"
if [[ -z "$git_hash" ]]; then
  git_hash="$(git -C "$repo" rev-parse --short=12 HEAD 2>/dev/null || true)"
fi
if [[ -z "$git_hash" ]]; then
  echo "AHA2_GIT_HASH is required when building outside a git checkout." >&2
  exit 2
fi
if [[ ! "$build_date" =~ ^[0-9]{8}$ ]]; then
  echo "Build date must use yyyymmdd format." >&2
  exit 2
fi
if [[ ! "$git_hash" =~ ^[0-9a-fA-F]{12}$ ]]; then
  echo "Git hash must contain exactly 12 hexadecimal characters." >&2
  exit 2
fi
git_hash="$(printf '%s' "$git_hash" | tr '[:upper:]' '[:lower:]')"
dirty=""
if [[ -z "$explicit_hash" && -n "$(git -C "$repo" status --porcelain 2>/dev/null || true)" ]]; then
  dirty=".dirty"
fi

printf 'v%s.%s.%s%s\n' "$base_version" "$build_date" "$git_hash" "$dirty"
