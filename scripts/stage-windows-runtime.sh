#!/usr/bin/env bash
set -euo pipefail

# Stages the Windows runtime files that the installer bundles but does not
# build itself:
#   <output>/browser-host/  host sources + the Playwright 1.49.0 package
#   <output>/node/          official Node.js 22.23.5 node.exe and LICENSE
#
# Release CI calls this before building the Windows installer, and
# scripts/build-windows-package.sh calls it for the local package. Keeping the
# file list and the pinned hashes here means the two paths cannot drift apart.
#
# The Node archive is verified against the official SHASUMS256.txt entry and
# the extracted node.exe against its own pin, so a substituted archive or a
# one-byte change fails before packaging. Playwright is installed with
# PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1: the browser host launches the owner's
# installed Edge, so no browser build belongs in the package.

usage() {
  cat <<'EOF'
Usage: bash scripts/stage-windows-runtime.sh [options]

Stage browser-host/ and node/ inside a Windows package output directory.

Options:
  --repo-dir DIR       Source checkout (default: the repository containing
                       this script)
  --output-dir DIR     Package directory (default: <repo>/dist)
  -h, --help           Show this help

Environment overrides:
  AHA2_NODE_RUNTIME_VERSION  bundled Node.js LTS version (default 22.23.5)
  AHA2_NODE_RUNTIME_CACHE    directory for the official archive (SHA-256 checked)
  AHA2_NODE_EXE              cached node.exe; must match the pinned version hash
  AHA2_NODE_LICENSE          license file to pair with AHA2_NODE_EXE
  AHA2_OSSLSIGNCODE          osslsigncode binary for real signature verification
  AHA2_NODE_SIGNATURE_POLICY warn (default) or require when no verifier exists
EOF
}

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_dir="$(cd -- "$script_dir/.." && pwd -P)"
output_dir=""

while (($# > 0)); do
  case "$1" in
    --repo-dir)
      [[ $# -ge 2 ]] || { printf 'Missing value for --repo-dir\n' >&2; exit 2; }
      repo_dir="$2"
      shift 2
      ;;
    --output-dir)
      [[ $# -ge 2 ]] || { printf 'Missing value for --output-dir\n' >&2; exit 2; }
      output_dir="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'Unknown option: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

repo_dir="$(cd -- "$repo_dir" && pwd -P)"
[[ -f "$repo_dir/browser-host/host.mjs" ]] || {
  printf 'RepoDir does not contain browser-host/host.mjs: %s\n' "$repo_dir" >&2
  exit 1
}
[[ -n "$output_dir" ]] || output_dir="$repo_dir/dist"
mkdir -p -- "$output_dir"
output_dir="$(cd -- "$output_dir" && pwd -P)"

command -v sha256sum >/dev/null 2>&1 || { printf 'sha256sum was not found.\n' >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { printf 'curl is required to verify the bundled Node runtime.\n' >&2; exit 1; }
zip_extract="$script_dir/extract-zip-member.sh"
[[ -f "$zip_extract" ]] || {
  printf 'ZIP extraction helper is missing: %s\n' "$zip_extract" >&2
  exit 1
}

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/aha2-runtime.XXXXXX")"
cleanup() {
  rm -rf -- "$work_dir"
}
trap cleanup EXIT

assert_pe() {
  local file="$1"
  [[ -f "$file" ]] || { printf 'Package artifact is missing: %s\n' "$file" >&2; exit 1; }
  [[ "$(LC_ALL=C head -c 2 "$file")" == "MZ" ]] || {
    printf 'Package artifact is not a Windows PE executable: %s\n' "$file" >&2
    exit 1
  }
}

fetch_node_shasums() {
  local sums="$1"
  curl -fsSL --retry 3 -o "$sums.tmp" \
    "https://nodejs.org/dist/v${node_runtime_version}/SHASUMS256.txt"
  mv -- "$sums.tmp" "$sums"
}

verify_node_signature() {
  local file="$1" work="$2"
  local verifier="${AHA2_OSSLSIGNCODE:-}"
  if [[ -z "$verifier" ]]; then
    verifier="$(command -v osslsigncode 2>/dev/null || true)"
  fi
  local signtool=""
  if [[ -z "$verifier" ]]; then
    signtool="$(command -v signtool.exe 2>/dev/null || command -v signtool 2>/dev/null || true)"
  fi
  mkdir -p -- "$work"
  if [[ -n "$verifier" ]]; then
    local openssl_bin=""
    openssl_bin="$(command -v openssl 2>/dev/null || true)"
    [[ -n "$openssl_bin" ]] || {
      printf 'openssl is required to verify the bundled node.exe signature.\n' >&2
      exit 1
    }
    "$verifier" extract-signature -in "$file" -out "$work/sig.der" >/dev/null 2>&1 || return 1
    "$openssl_bin" pkcs7 -inform DER -in "$work/sig.der" -print_certs \
      -out "$work/certs.pem" 2>/dev/null || return 1
    mkdir -p -- "$work/certs"
    awk -v dir="$work/certs" '/BEGIN CERTIFICATE/{n++} n > 0 {print > (dir "/cert-" n ".pem")}' \
      "$work/certs.pem"
    : > "$work/ca.pem"
    local cert subject issuer
    for cert in "$work"/certs/cert-*.pem; do
      subject="$("$openssl_bin" x509 -in "$cert" -noout -subject | sed 's/^subject=//')"
      issuer="$("$openssl_bin" x509 -in "$cert" -noout -issuer | sed 's/^issuer=//')"
      if [[ "$subject" == "$issuer" ]]; then
        cat "$cert" >> "$work/ca.pem"
      fi
    done
    [[ -s "$work/ca.pem" ]] || return 1
    "$verifier" verify -in "$file" -CAfile "$work/ca.pem" -TSA-CAfile "$work/ca.pem" \
      > "$work/verify.log" 2>&1 || return 1
    grep -q "OpenJS Foundation" "$work/verify.log" || return 1
    return 0
  fi
  if [[ -n "$signtool" ]]; then
    "$signtool" verify /pa /v "$file" > "$work/verify.log" 2>&1 || return 1
    grep -q "OpenJS Foundation" "$work/verify.log" || return 1
    return 0
  fi
  return 2
}

# Git for Windows does not always ship unzip, and its PATH can put GNU tar
# ahead of a usable bsdtar. The helper probes every unzip/bsdtar/tar candidate
# and fails with a clear message when none can read the archive.
extract_zip_member() {
  local archive="$1" member="$2" dest="$3"
  bash "$zip_extract" "$archive" "$member" > "$dest"
  [[ -s "$dest" ]] || {
    printf 'Node runtime archive member is missing: %s\n' "$member" >&2
    exit 1
  }
}

stage_node_runtime() {
  node_runtime_version="${AHA2_NODE_RUNTIME_VERSION:-22.23.5}"
  local node_out="$output_dir/node"
  rm -rf -- "$node_out"
  mkdir -p -- "$node_out"

  # Pins for the default runtime: zip hash from nodejs.org SHASUMS256.txt and
  # the node.exe hash after extracting that exact archive. Update both
  # together.
  local pin_zip_sha=""
  local pin_exe_sha=""
  case "$node_runtime_version" in
    22.23.5)
      pin_zip_sha="4a191082e9706866830379d577d2cc8a50c776fcd9436a8d061b063ed0d9a0e3"
      pin_exe_sha="247e9693d316da9857e44990e5e11709f596f18112251b8026ec6eed80bdf38a"
      ;;
  esac

  local archive_name="node-v${node_runtime_version}-win-x64.zip"
  local expected_sha actual_sha

  if [[ -n "${AHA2_NODE_EXE:-}" ]]; then
    [[ -f "$AHA2_NODE_EXE" ]] || {
      printf 'AHA2_NODE_EXE does not exist: %s\n' "$AHA2_NODE_EXE" >&2
      exit 1
    }
    [[ -n "$pin_exe_sha" ]] || {
      printf 'AHA2_NODE_EXE is only accepted for a pinned Node runtime version (got %s).\n' \
        "$node_runtime_version" >&2
      printf 'Use AHA2_NODE_RUNTIME_CACHE with the official archive, or update the pin.\n' >&2
      exit 1
    }
    actual_sha="$(sha256sum "$AHA2_NODE_EXE" | awk '{print $1}')"
    [[ "$actual_sha" == "$pin_exe_sha" ]] || {
      printf 'Cached node.exe SHA-256 mismatch for %s: got %s want %s\n' \
        "$node_runtime_version" "$actual_sha" "$pin_exe_sha" >&2
      exit 1
    }
    mkdir -p -- "$work_dir/node-runtime-cache"
    local node_sums="$work_dir/node-runtime-cache/SHASUMS256.txt"
    fetch_node_shasums "$node_sums"
    expected_sha="$(awk -v name="$archive_name" '$2 == name {print $1}' "$node_sums")"
    [[ "$expected_sha" == "$pin_zip_sha" ]] || {
      printf 'Official Node archive checksum changed for %s: got %s want %s\n' \
        "$node_runtime_version" "$expected_sha" "$pin_zip_sha" >&2
      exit 1
    }
    cp -a -- "$AHA2_NODE_EXE" "$node_out/node.exe"
    if [[ -n "${AHA2_NODE_LICENSE:-}" ]]; then
      cp -a -- "$AHA2_NODE_LICENSE" "$node_out/LICENSE"
    fi
  else
    local node_cache="${AHA2_NODE_RUNTIME_CACHE:-$work_dir/node-runtime-cache}"
    mkdir -p -- "$node_cache"
    local node_archive="$node_cache/$archive_name"
    local node_sums="$node_cache/SHASUMS256.txt"
    fetch_node_shasums "$node_sums"
    expected_sha="$(awk -v name="$archive_name" '$2 == name {print $1}' "$node_sums")"
    [[ -n "$expected_sha" ]] || {
      printf 'Node runtime checksum is missing from SHASUMS256.txt: %s\n' "$archive_name" >&2
      exit 1
    }
    if [[ -n "$pin_zip_sha" && "$expected_sha" != "$pin_zip_sha" ]]; then
      printf 'Official Node archive checksum changed for %s: got %s want %s\n' \
        "$node_runtime_version" "$expected_sha" "$pin_zip_sha" >&2
      exit 1
    fi
    if [[ ! -f "$node_archive" ]]; then
      printf 'Downloading %s\n' "https://nodejs.org/dist/v${node_runtime_version}/${archive_name}"
      curl -fsSL --retry 3 -o "$node_archive.download" \
        "https://nodejs.org/dist/v${node_runtime_version}/${archive_name}"
      mv -- "$node_archive.download" "$node_archive"
    fi
    actual_sha="$(sha256sum "$node_archive" | awk '{print $1}')"
    [[ "$actual_sha" == "$expected_sha" ]] || {
      printf 'Node runtime archive checksum mismatch: got %s want %s\n' "$actual_sha" "$expected_sha" >&2
      exit 1
    }
    local node_prefix="node-v${node_runtime_version}-win-x64"
    extract_zip_member "$node_archive" "$node_prefix/node.exe" "$node_out/node.exe"
    extract_zip_member "$node_archive" "$node_prefix/LICENSE" "$node_out/LICENSE"
    if [[ -n "$pin_exe_sha" ]]; then
      actual_sha="$(sha256sum "$node_out/node.exe" | awk '{print $1}')"
      [[ "$actual_sha" == "$pin_exe_sha" ]] || {
        printf 'Extracted node.exe SHA-256 mismatch for %s: got %s want %s\n' \
          "$node_runtime_version" "$actual_sha" "$pin_exe_sha" >&2
        exit 1
      }
    fi
  fi

  [[ -s "$node_out/node.exe" ]] || {
    printf 'Bundled node.exe was not staged.\n' >&2
    exit 1
  }
  [[ -s "$node_out/LICENSE" ]] || {
    printf 'Bundled Node LICENSE was not staged.\n' >&2
    exit 1
  }
  assert_pe "$node_out/node.exe"
  local signature_status=0
  verify_node_signature "$node_out/node.exe" "$work_dir/node-signature" || signature_status=$?
  if [[ "$signature_status" == 0 ]]; then
    printf 'Bundled node.exe signature verified (OpenJS Foundation)\n'
  elif [[ "$signature_status" == 2 ]]; then
    if [[ "${AHA2_NODE_SIGNATURE_POLICY:-warn}" == "require" ]]; then
      printf 'Bundled node.exe signature could not be verified and AHA2_NODE_SIGNATURE_POLICY=require.\n' >&2
      exit 1
    fi
    printf 'WARNING: bundled node.exe Authenticode signature was NOT verified (osslsigncode/signtool unavailable); SHA-256 still pins the official archive.\n' >&2
  else
    printf 'Bundled node.exe signature verification failed.\n' >&2
    exit 1
  fi
  printf 'Bundled Node runtime: %s (%s)\n' "$node_runtime_version" "$(du -h "$node_out/node.exe" | cut -f1)"
}

stage_browser_host() {
  local browser_host_out="$output_dir/browser-host"
  rm -rf -- "$browser_host_out"
  mkdir -p -- "$browser_host_out"
  local host_file
  for host_file in host.mjs server.mjs tabs.mjs interact.mjs downloads.mjs cookies.mjs lock.mjs package.json README.md; do
    [[ -f "$repo_dir/browser-host/$host_file" ]] || {
      printf 'Browser host source is missing: %s\n' "$repo_dir/browser-host/$host_file" >&2
      exit 1
    }
    cp -a -- "$repo_dir/browser-host/$host_file" "$browser_host_out/$host_file"
  done
  command -v npm >/dev/null 2>&1 || {
    printf 'npm was not found; it is needed to stage the browser host dependency.\n' >&2
    exit 1
  }
  (
    cd "$browser_host_out"
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm install --omit=dev --ignore-scripts \
      --no-audit --no-fund --no-save --package-lock=false playwright@1.49.0
  )
  rm -rf -- "$browser_host_out/node_modules/.bin"
  # npm's internal metadata is not needed at runtime and would only add hidden
  # files to the installer manifest.
  rm -f -- "$browser_host_out/node_modules/.package-lock.json"
  [[ -f "$browser_host_out/node_modules/playwright/package.json" ]] || {
    printf 'browser host Playwright dependency was not installed.\n' >&2
    exit 1
  }
}

printf 'Repository: %s\n' "$repo_dir"
printf 'Output: %s\n' "$output_dir"

printf '==> Stage bundled Node runtime\n'
stage_node_runtime

printf '==> Stage browser host\n'
stage_browser_host

printf 'Windows runtime staged: %s\n' "$output_dir"
printf '  %s\n' \
  "$output_dir/browser-host/host.mjs" \
  "$output_dir/browser-host/node_modules/playwright/package.json" \
  "$output_dir/node/node.exe" \
  "$output_dir/node/LICENSE"
