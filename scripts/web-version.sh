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
git_hash="${AHA2_GIT_HASH:-$(git -C "$repo" rev-parse --short=12 HEAD)}"
if [[ ! "$build_date" =~ ^[0-9]{8}$ ]]; then
  echo "Build date must use yyyymmdd format." >&2
  exit 2
fi
if [[ ! "$git_hash" =~ ^[0-9a-fA-F]{12}$ ]]; then
  echo "Git hash must contain exactly 12 hexadecimal characters." >&2
  exit 2
fi
git_hash="${git_hash,,}"
dirty=""
if [[ -n "$(git -C "$repo" status --porcelain)" ]]; then
  dirty=".dirty"
fi

printf 'v%s.%s.%s%s\n' "$base_version" "$build_date" "$git_hash" "$dirty"
