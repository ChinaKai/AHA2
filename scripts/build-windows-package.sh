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

assert_pe() {
  local file="$1"
  [[ -f "$file" ]] || { printf 'Package artifact is missing: %s\n' "$file" >&2; exit 1; }
  [[ "$(LC_ALL=C head -c 2 "$file")" == "MZ" ]] || {
    printf 'Package artifact is not a Windows PE executable: %s\n' "$file" >&2
    exit 1
  }
}
assert_pe "$server_exe"
assert_pe "$tray_exe"
assert_pe "$feishu_exe"
[[ -f "$feishu_manifest" ]] || { printf 'Feishu plugin manifest is missing: %s\n' "$feishu_manifest" >&2; exit 1; }
if grep -q "SET_BY_PACKAGING" "$feishu_manifest"; then
  printf 'Feishu plugin manifest still contains the packaging placeholder: %s\n' "$feishu_manifest" >&2
  exit 1
fi

printf '==> Write release manifest\n'
# Record the full invocation so replaying from a source snapshot (no .git) can
# reproduce the same package: the commit and test options are part of the
# generator description, and the output path is shell-quoted.
generator_command="bash scripts/build-windows-package.sh --version $version --commit $commit --output-dir $(printf '%q' "$output_dir")"
if ((run_web_test == 0)); then
  generator_command+=" --skip-web-test"
fi
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
  aha2-windows-amd64.exe \
  aha2-tray-windows-amd64.exe \
  plugins/feishu/windows-amd64/aha2-channel-feishu.exe \
  plugins/feishu/windows-amd64/plugin.json

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
];
if (manifest.version !== version) throw new Error(`manifest version ${manifest.version} != ${version}`);
if (manifest.commit !== commit) throw new Error(`manifest commit ${manifest.commit} != ${commit}`);
const paths = manifest.files.map((file) => file.path);
for (const path of expected) {
  if (!paths.includes(path)) throw new Error(`manifest is missing ${path}`);
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
