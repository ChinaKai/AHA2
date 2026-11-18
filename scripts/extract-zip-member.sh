#!/usr/bin/env bash
set -euo pipefail

# Writes one member of a zip archive to stdout.
#
# Usage: bash scripts/extract-zip-member.sh ARCHIVE MEMBER
#
# `unzip` is preferred when present. Git for Windows often has no unzip, and
# its PATH may put GNU tar ahead of a usable bsdtar; GNU tar cannot read zip.
# Every unzip/bsdtar/tar on PATH is therefore probed in order, and the first
# tool that can list the archive extracts the member. If none can, the command
# fails with a message that names both alternatives instead of assuming the
# first tar on PATH is the right one.

if (($# != 2)); then
  printf 'Usage: %s ARCHIVE MEMBER\n' "$0" >&2
  exit 2
fi
archive="$1"
member="$2"
[[ -f "$archive" ]] || { printf 'Archive not found: %s\n' "$archive" >&2; exit 1; }
command -v mktemp >/dev/null 2>&1 || { printf 'mktemp is required to extract %s.\n' "$archive" >&2; exit 1; }
command -v cat >/dev/null 2>&1 || { printf 'cat is required to extract %s.\n' "$archive" >&2; exit 1; }

# Emit extractor candidates in probe order: unzip first, then each PATH entry
# in order. Both bare and .exe names are checked because Git Bash resolves
# Windows executables that way.
tool_candidates() {
  local unzip_bin dir candidate
  unzip_bin="$(command -v unzip 2>/dev/null || true)"
  if [[ -n "$unzip_bin" ]]; then
    printf '%s\n' "$unzip_bin"
  fi
  local old_ifs="$IFS"
  IFS=:
  for dir in $PATH; do
    IFS="$old_ifs"
    [[ -n "$dir" ]] || dir=.
    for candidate in unzip unzip.exe bsdtar bsdtar.exe tar tar.exe; do
      if [[ -f "$dir/$candidate" && -x "$dir/$candidate" ]]; then
        printf '%s\n' "$dir/$candidate"
      fi
    done
    IFS=:
  done
  IFS="$old_ifs"
}

candidates=()
declare -A seen=()
while IFS= read -r tool; do
  [[ -n "$tool" ]] || continue
  if [[ -n "${seen[$tool]:-}" ]]; then
    continue
  fi
  seen[$tool]=1
  candidates+=("$tool")
done < <(tool_candidates)

tmp="$(mktemp "${TMPDIR:-/tmp}/aha2-zip-member.XXXXXX")"
trap 'rm -f -- "$tmp"' EXIT

for tool in "${candidates[@]}"; do
  name="${tool##*/}"
  : > "$tmp"
  case "$name" in
    unzip|unzip.exe)
      if "$tool" -p "$archive" "$member" > "$tmp" 2>/dev/null && [[ -s "$tmp" ]]; then
        cat "$tmp"
        exit 0
      fi
      ;;
    *)
      # bsdtar lists and extracts zip archives; GNU tar fails the list probe,
      # so the next candidate is tried.
      if "$tool" -tf "$archive" >/dev/null 2>&1 &&
        "$tool" -xOf "$archive" "$member" > "$tmp" 2>/dev/null &&
        [[ -s "$tmp" ]]; then
        cat "$tmp"
        exit 0
      fi
      ;;
  esac
done

printf 'No ZIP extractor could read %s: install unzip or bsdtar, or put one on PATH.\n' "$archive" >&2
exit 1
