#!/usr/bin/env bash
set -euo pipefail

# Builds the Windows release package consumed by the installer on the target
# machine. The repository only compiles and packages here: this script never
# stops a service, copies installed files, touches a data directory or restarts
# anything. Installing, backing up, replacing, rolling back and health checks
# belong to the host installer.
#
# The build runs in a temporary copy of the source tree because the Web build
# writes web/dist and the embedded-asset directory. Copying keeps the source
# checkout untouched; only --output-dir is written in place.
#
# Output (under --output-dir, default dist/):
#   aha2-windows-amd64.exe
#   aha2-tray-windows-amd64.exe
#   plugins/feishu/windows-amd64/aha2-channel-feishu.exe
#   plugins/feishu/windows-amd64/plugin.json
#   browser-host/ (host sources + Playwright Node package)
#   node/node.exe + node/LICENSE (bundled Node.js LTS runtime)
#   release-manifest.json

usage() {
  cat <<'EOF'
Usage: bash scripts/build-windows-package.sh [options]

Build the Windows release package and its release-manifest.json in a temporary
copy of the source tree. This script only builds; deployment belongs to the host
installer, which needs no Go or Node toolchain.

Options:
  --version VERSION    Override the package version (default: VERSION env,
                       then the nearest git tag, then 0.0.0)
  --output-dir DIR     Package directory (default: <repo>/dist)
  --commit SHA         Commit recorded in the manifest (default: git rev-parse HEAD;
                       also supplies the 12-character build hash outside a git checkout)
  --skip-web-test      Do not run web/tests/build.test.mjs
  -h, --help           Show this help

Environment overrides:
  VERSION, GO_BIN, AHA2_BUILD_DATE, AHA2_GIT_HASH
  AHA2_NODE_RUNTIME_VERSION  bundled Node.js LTS version (default 22.23.5)
  AHA2_NODE_RUNTIME_CACHE    directory for the official archive (SHA-256 checked)
  AHA2_NODE_EXE              cached node.exe; must match the pinned version hash
  AHA2_NODE_LICENSE          license file to pair with AHA2_NODE_EXE
  AHA2_OSSLSIGNCODE          osslsigncode binary for real signature verification
  AHA2_NODE_SIGNATURE_POLICY warn (default) or require when no verifier exists
EOF
}

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
version="${VERSION:-}"
output_dir="$repo_dir/dist"
commit=""
commit_given=0
run_web_test=1

while (($# > 0)); do
  case "$1" in
    --version)
      [[ $# -ge 2 ]] || { printf 'Missing value for --version\n' >&2; exit 2; }
      version="$2"
      shift 2
      ;;
    --output-dir)
      [[ $# -ge 2 ]] || { printf 'Missing value for --output-dir\n' >&2; exit 2; }
      output_dir="$2"
      shift 2
      ;;
    --commit)
      [[ $# -ge 2 ]] || { printf 'Missing value for --commit\n' >&2; exit 2; }
      commit="$2"
      commit_given=1
      shift 2
      ;;
    --skip-web-test)
      run_web_test=0
      shift
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

if [[ -z "$version" ]]; then
  version="$(git -C "$repo_dir" describe --tags --abbrev=0 2>/dev/null || true)"
fi
version="${version#v}"
[[ -n "$version" ]] || version="0.0.0"
if [[ ! "$version" =~ ^[0-9]+([.][0-9]+){2}$ ]]; then
  printf 'Version must use x.y.z format: %s\n' "$version" >&2
  exit 2
fi

if ((commit_given)); then
  [[ "$commit" =~ ^[0-9a-fA-F]{12,40}$ ]] || {
    printf 'Commit must be 12 to 40 hexadecimal characters: %s\n' "$commit" >&2
    exit 2
  }
else
  commit="$(git -C "$repo_dir" rev-parse HEAD 2>/dev/null || true)"
  [[ -n "$commit" ]] || { printf 'Commit is required outside a git checkout; pass --commit.\n' >&2; exit 2; }
fi
commit_hash="$(printf '%s' "${commit:0:12}" | tr '[:upper:]' '[:lower:]')"

# The Web build calls module.stripTypeScriptTypes, which Node added in 22.13.0 and
# 23.2.0 and removed again in 26. Check the capability instead of the version so
# an interpreter that only looks new enough fails at the check, not deep inside
# build-web.mjs.
node_can_build_web() {
  "$1" -e 'const m = require("node:module"); process.exit(typeof m.stripTypeScriptTypes === "function" ? 0 : 1);' >/dev/null 2>&1
}

node_bin=""
if candidate="$(command -v node 2>/dev/null)" && node_can_build_web "$candidate"; then
  node_bin="$candidate"
else
  while IFS= read -r candidate; do
    [[ -x "$candidate" ]] || continue
    if node_can_build_web "$candidate"; then
      node_bin="$candidate"
      break
    fi
  done < <(ls -1d "$HOME"/.nvm/versions/node/*/bin/node /usr/local/bin/node 2>/dev/null | sort -Vr)
fi
[[ -n "$node_bin" ]] || {
  printf 'No Node.js interpreter able to run the Web build was found.\n' >&2
  printf 'It needs module.stripTypeScriptTypes with {mode:"transform"}, which is\n' >&2
  printf 'present in Node 22.13+/23.2+ and removed again in Node 26.\n' >&2
  printf 'On PATH: %s\n' "$(node --version 2>/dev/null || printf 'no node')" >&2
  exit 1
}

# GO_BIN may be an absolute path or a bare command name (release CI passes
# GO_BIN=go). Resolve bare names through PATH instead of testing the literal
# string as a file.
if [[ -n "${GO_BIN:-}" ]]; then
  if [[ "$GO_BIN" == */* ]]; then
    go_bin="$GO_BIN"
  else
    go_bin="$(command -v "$GO_BIN" 2>/dev/null || true)"
  fi
elif [[ -x "$repo_dir/.tools/go/bin/go" ]]; then
  go_bin="$repo_dir/.tools/go/bin/go"
else
  go_bin="$(command -v go || true)"
fi
[[ -n "$go_bin" && -x "$go_bin" ]] || {
  printf 'Go toolchain was not found: GO_BIN=%s\n' "${GO_BIN:-}" >&2
  exit 1
}
command -v sha256sum >/dev/null 2>&1 || { printf 'sha256sum was not found.\n' >&2; exit 1; }
command -v tar >/dev/null 2>&1 || { printf 'tar was not found.\n' >&2; exit 1; }

output_dir="$(realpath -m "$output_dir")"
mkdir -p -- "$output_dir"

# Copy the source tree instead of building in place. web/dist and
# internal/webassets/dist are generated directories; writing them inside the
# checkout would leave build output behind and make the "only the output
# directory is written" promise false.
build_dir="$(mktemp -d "${TMPDIR:-/tmp}/aha2-build.XXXXXX")"
cleanup() {
  rm -rf -- "$build_dir"
}
trap cleanup EXIT

printf '==> Copy sources to a build directory\n'
tar -C "$repo_dir" -cf - \
  --exclude=.git \
  --exclude=.aha2-context \
  --exclude=.tools \
  --exclude=tmp \
  --exclude=dist \
  --exclude=node_modules \
  --exclude=web/build \
  . | tar -C "$build_dir" -xf -

built_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if ((commit_given)); then
  web_version="$(AHA2_GIT_HASH="$commit_hash" bash "$repo_dir/scripts/web-version.sh" "$version")"
else
  web_version="$(bash "$repo_dir/scripts/web-version.sh" "$version")"
fi
go_version="$("$go_bin" version | awk '{print $3}')"
node_version="$("$node_bin" --version)"
server_exe="$output_dir/aha2-windows-amd64.exe"
tray_exe="$output_dir/aha2-tray-windows-amd64.exe"
feishu_dir="$output_dir/plugins/feishu/windows-amd64"
feishu_exe="$feishu_dir/aha2-channel-feishu.exe"
feishu_manifest="$feishu_dir/plugin.json"
manifest_path="$output_dir/release-manifest.json"

printf 'Repository: %s\n' "$repo_dir"
printf 'Build dir: %s\n' "$build_dir"
printf 'Version: %s\n' "$version"
printf 'Web version: %s\n' "$web_version"
printf 'Commit: %s (%s)\n' "$commit" "$commit_hash"
printf 'Output: %s\n' "$output_dir"
printf 'Node: %s (%s)\n' "$node_bin" "$node_version"
printf 'Go: %s (%s)\n' "$go_bin" "$go_version"

rm -f -- "$server_exe" "$tray_exe" "$feishu_exe" "$manifest_path"

assert_pe() {
  local file="$1"
  [[ -f "$file" ]] || { printf 'Package artifact is missing: %s\n' "$file" >&2; exit 1; }
  [[ "$(LC_ALL=C head -c 2 "$file")" == "MZ" ]] || {
    printf 'Package artifact is not a Windows PE executable: %s\n' "$file" >&2
    exit 1
  }
}

# A clean Windows machine must not need a system Node/npm install. Ship the
# official Node.js LTS runtime next to the host and let AHA find it at
# <install>/node/node.exe. Only node.exe and its license are staged; Playwright
# lives in browser-host/node_modules and the host launches the installed Edge.
#
# Integrity: the official archive is checked against the selected version's
# SHASUMS256.txt. nodejs.org does not publish a per-file node.exe checksum, so
# the AHA2_NODE_EXE cache path is pinned to the exact node.exe hash extracted
# from that archive; a one-byte change fails. Signature verification uses a
# real verifier (osslsigncode/signtool) when available and never treats a
# certificate listing as verification.
printf '==> Stage bundled Node runtime\n'
node_runtime_version="${AHA2_NODE_RUNTIME_VERSION:-22.23.5}"
node_out="$output_dir/node"
rm -rf -- "$node_out"
mkdir -p -- "$node_out"

# Pins for the default runtime: zip hash from nodejs.org SHASUMS256.txt and the
# node.exe hash after extracting that exact archive. Update both together.
node_runtime_zip_sha=""
node_runtime_exe_sha=""
case "$node_runtime_version" in
  22.23.5)
    node_runtime_zip_sha="4a191082e9706866830379d577d2cc8a50c776fcd9436a8d061b063ed0d9a0e3"
    node_runtime_exe_sha="247e9693d316da9857e44990e5e11709f596f18112251b8026ec6eed80bdf38a"
    ;;
esac

fetch_node_shasums() {
  local sums="$1"
  command -v curl >/dev/null 2>&1 || {
    printf 'curl is required to verify the bundled Node runtime.\n' >&2
    exit 1
  }
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

if [[ -n "${AHA2_NODE_EXE:-}" ]]; then
  [[ -f "$AHA2_NODE_EXE" ]] || {
    printf 'AHA2_NODE_EXE does not exist: %s\n' "$AHA2_NODE_EXE" >&2
    exit 1
  }
  [[ -n "$node_runtime_exe_sha" ]] || {
    printf 'AHA2_NODE_EXE is only accepted for a pinned Node runtime version (got %s).\n' \
      "$node_runtime_version" >&2
    printf 'Use AHA2_NODE_RUNTIME_CACHE with the official archive, or update the pin.\n' >&2
    exit 1
  }
  actual_exe_sha="$(sha256sum "$AHA2_NODE_EXE" | awk '{print $1}')"
  [[ "$actual_exe_sha" == "$node_runtime_exe_sha" ]] || {
    printf 'Cached node.exe SHA-256 mismatch for %s: got %s want %s\n' \
      "$node_runtime_version" "$actual_exe_sha" "$node_runtime_exe_sha" >&2
    exit 1
  }
  node_sums="$build_dir/node-runtime-cache/SHASUMS256.txt"
  mkdir -p -- "$build_dir/node-runtime-cache"
  fetch_node_shasums "$node_sums"
  archive_expected="$(awk -v name="node-v${node_runtime_version}-win-x64.zip" \
    '$2 == name {print $1}' "$node_sums")"
  [[ "$archive_expected" == "$node_runtime_zip_sha" ]] || {
    printf 'Official Node archive checksum changed for %s: got %s want %s\n' \
      "$node_runtime_version" "$archive_expected" "$node_runtime_zip_sha" >&2
    exit 1
  }
  cp -a -- "$AHA2_NODE_EXE" "$node_out/node.exe"
  if [[ -n "${AHA2_NODE_LICENSE:-}" ]]; then
    cp -a -- "$AHA2_NODE_LICENSE" "$node_out/LICENSE"
  fi
else
  command -v unzip >/dev/null 2>&1 || {
    printf 'unzip is required to stage the bundled Node runtime.\n' >&2
    exit 1
  }
  node_cache="${AHA2_NODE_RUNTIME_CACHE:-$build_dir/node-runtime-cache}"
  mkdir -p -- "$node_cache"
  archive_name="node-v${node_runtime_version}-win-x64.zip"
  node_archive="$node_cache/$archive_name"
  node_sums="$node_cache/SHASUMS256.txt"
  fetch_node_shasums "$node_sums"
  if [[ ! -f "$node_archive" ]]; then
    printf 'Downloading %s\n' "https://nodejs.org/dist/v${node_runtime_version}/${archive_name}"
    curl -fsSL --retry 3 -o "$node_archive.download" \
      "https://nodejs.org/dist/v${node_runtime_version}/${archive_name}"
    mv -- "$node_archive.download" "$node_archive"
  fi
  expected_sha="$(awk -v name="$archive_name" '$2 == name {print $1}' "$node_sums")"
  [[ -n "$expected_sha" ]] || {
    printf 'Node runtime checksum is missing from SHASUMS256.txt: %s\n' "$archive_name" >&2
    exit 1
  }
  actual_sha="$(sha256sum "$node_archive" | awk '{print $1}')"
  [[ "$actual_sha" == "$expected_sha" ]] || {
    printf 'Node runtime archive checksum mismatch: got %s want %s\n' "$actual_sha" "$expected_sha" >&2
    exit 1
  }
  node_prefix="node-v${node_runtime_version}-win-x64"
  unzip -p "$node_archive" "$node_prefix/node.exe" > "$node_out/node.exe"
  unzip -p "$node_archive" "$node_prefix/LICENSE" > "$node_out/LICENSE"
  if [[ -n "$node_runtime_exe_sha" ]]; then
    actual_exe_sha="$(sha256sum "$node_out/node.exe" | awk '{print $1}')"
    [[ "$actual_exe_sha" == "$node_runtime_exe_sha" ]] || {
      printf 'Extracted node.exe SHA-256 mismatch for %s: got %s want %s\n' \
        "$node_runtime_version" "$actual_exe_sha" "$node_runtime_exe_sha" >&2
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
signature_status=0
verify_node_signature "$node_out/node.exe" "$build_dir/node-signature" || signature_status=$?
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


printf '==> Build Web assets\n'
# The staged tree omits node_modules, and the Web build needs the esbuild
# version pinned in web/package-lock.json. Install it on the build host first.
if ! command -v npm >/dev/null 2>&1; then
  printf 'npm was not found; install it next to Node and run `npm ci --prefix web`.\n' >&2
  exit 1
fi
(cd "$build_dir/web" && npm ci --no-audit --no-fund)
"$node_bin" "$build_dir/scripts/build-web.mjs"
if ((run_web_test)); then
  printf '==> Test Web build\n'
  (cd "$build_dir" && "$node_bin" --test "$build_dir/web/tests/build.test.mjs")
fi

printf '==> Sync embedded Web assets\n'
mkdir -p -- "$build_dir/internal/webassets/dist"
find "$build_dir/internal/webassets/dist" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
cp -a -- "$build_dir/web/dist/." "$build_dir/internal/webassets/dist/"

printf '==> Build Windows server\n'
(
  cd "$build_dir"
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 "$go_bin" build -buildvcs=false -trimpath \
    -ldflags="-s -w -X main.version=$version -X main.webVersion=$web_version" \
    -o "$server_exe" ./cmd/aha
)
printf '==> Build Windows tray\n'
(
  cd "$build_dir"
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 "$go_bin" build -buildvcs=false -trimpath \
    -ldflags="-s -w -H windowsgui -X main.version=$version" \
    -o "$tray_exe" ./cmd/aha-tray
)

# The Feishu plugin is a nested Go module; neither the root `go test ./...` nor
# the plugin compile reaches its tests, so the package build runs them itself.
# The success marker is printed only after the captured test output reports an
# ok package, so deleting the execution line fails the build instead of leaving
# a green banner behind.
printf '==> Test Feishu plugin\n'
plugin_test_output="$(cd "$build_dir/plugins/feishu" && "$go_bin" test ./... 2>&1)" || {
  printf '%s\n' "$plugin_test_output" >&2
  printf 'Feishu plugin tests failed.\n' >&2
  exit 1
}
printf '%s\n' "$plugin_test_output"
# Either an ok package or an explicit "no test files" result is evidence that
# `go test` ran; empty output means the invocation was removed.
if ! grep -Eq '^(ok|\?)[[:space:]]' <<<"$plugin_test_output"; then
  printf 'Feishu plugin tests did not report a package result.\n' >&2
  exit 1
fi
printf '==> Vet Feishu plugin\n'
(cd "$build_dir/plugins/feishu" && "$go_bin" vet ./...)
printf '==> Feishu plugin tests passed\n'
printf '==> Build Feishu plugin\n'
GO_BIN="$go_bin" AHA2_PLUGIN_OUTPUT_DIR="$feishu_dir" \
  bash "$build_dir/scripts/build-feishu-plugin.sh" windows/amd64

assert_pe "$server_exe"
assert_pe "$tray_exe"
assert_pe "$feishu_exe"
[[ -f "$feishu_manifest" ]] || { printf 'Feishu plugin manifest is missing: %s\n' "$feishu_manifest" >&2; exit 1; }
if grep -q "SET_BY_PACKAGING" "$feishu_manifest"; then
  printf 'Feishu plugin manifest still contains the packaging placeholder: %s\n' "$feishu_manifest" >&2
  exit 1
fi

# AHA auto-starts the browser host from <install>/browser-host/host.mjs. Ship the
# host sources plus the Playwright Node package (not its browser downloads: the
# host launches the owner's installed Edge via executablePath). The Node
# runtime itself is staged above into <install>/node/node.exe.
printf '==> Stage browser host\n'
browser_host_out="$output_dir/browser-host"
rm -rf -- "$browser_host_out"
mkdir -p -- "$browser_host_out"
for host_file in host.mjs server.mjs tabs.mjs lock.mjs package.json README.md; do
  cp -a -- "$build_dir/browser-host/$host_file" "$browser_host_out/$host_file"
done
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


printf '==> Write release manifest\n'
# Record the full invocation so replaying from a source snapshot (no .git) can
# reproduce the same package: the commit and test options are part of the
# generator description, and the output path is shell-quoted.
generator_command="bash scripts/build-windows-package.sh --version $version --commit $commit --output-dir $(printf '%q' "$output_dir")"
if ((run_web_test == 0)); then
  generator_command+=" --skip-web-test"
fi
package_files=(
  aha2-windows-amd64.exe
  aha2-tray-windows-amd64.exe
  plugins/feishu/windows-amd64/aha2-channel-feishu.exe
  plugins/feishu/windows-amd64/plugin.json
)
while IFS= read -r rel; do
  package_files+=("$rel")
done < <(cd "$output_dir" && find browser-host node -type f | LC_ALL=C sort)

"$node_bin" "$build_dir/scripts/release-manifest.mjs" \
  --root "$output_dir" \
  --version "$version" \
  --commit "$commit" \
  --built-at "$built_at" \
  --output "$manifest_path" \
  --generator-script "scripts/build-windows-package.sh" \
  --generator-command "$generator_command" \
  --web-version "$web_version" \
  --go-version "$go_version" \
  --node-version "$node_version" \
  "${package_files[@]}"

printf '==> Verify release manifest\n'
"$node_bin" - "$manifest_path" "$version" "$commit" <<'NODE'
const fs = require("node:fs");
const [manifestPath, version, commit] = process.argv.slice(2);
const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
const expected = [
  "aha2-windows-amd64.exe",
  "aha2-tray-windows-amd64.exe",
  "plugins/feishu/windows-amd64/aha2-channel-feishu.exe",
  "plugins/feishu/windows-amd64/plugin.json",
  "browser-host/host.mjs",
  "browser-host/server.mjs",
  "browser-host/package.json",
  "browser-host/node_modules/playwright/package.json",
  "node/node.exe",
  "node/LICENSE",
];
if (manifest.version !== version) throw new Error(`manifest version ${manifest.version} != ${version}`);
if (manifest.commit !== commit) throw new Error(`manifest commit ${manifest.commit} != ${commit}`);
const paths = manifest.files.map((file) => file.path);
for (const path of expected) {
  if (!paths.includes(path)) throw new Error(`manifest is missing ${path}`);
}
// Every packaged browser-host and Node runtime file must be covered by the
// manifest so the installer can verify the exact install tree.
const {execFileSync} = require("node:child_process");
for (const root of ["browser-host", "node"]) {
  const files = execFileSync("find", [root, "-type", "f"], {
    cwd: require("node:path").dirname(manifestPath), encoding: "utf8",
  }).trim().split("\n").filter(Boolean);
  for (const path of files) {
    if (!paths.includes(path)) throw new Error(`manifest is missing ${path}`);
  }
}
for (const file of manifest.files) {
  if (!/^[0-9a-f]{64}$/.test(file.sha256)) throw new Error(`manifest has a malformed sha256 for ${file.path}`);
}
if (!manifest.generator || manifest.generator.script !== "scripts/build-windows-package.sh") {
  throw new Error("manifest generator.script is not scripts/build-windows-package.sh");
}
NODE

printf 'Package ready: %s\n' "$output_dir"
printf '  %s\n' "$server_exe" "$tray_exe" "$feishu_exe" "$feishu_manifest" "$manifest_path"
