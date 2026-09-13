#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'EOF'
Usage: ./local_build.sh [options]

Build, package, validate, deploy, and restart the Windows AHA2 per-user instance.

Options:
  --version VERSION   Override AHA2_VERSION (default: 0.5.22)
  --validate-only     Validate the local toolchain and Windows deployment target
  --build-only        Build, verify, and package candidates without deploying
  --deploy-only       Deploy the existing verified candidates
  -h, --help          Show this help

Environment overrides:
  AHA2_VERSION
  AHA2_INSTALL_DIR
  AHA2_DATA_DIR
  AHA2_LISTEN
  AHA2_HEALTH_URL
  AHA2_AGENT_API_URL
  AHA2_RESULT_PATH
  AHA2_INSTALLER_DIR
  AHA2_ISCC_PATH
  AHA2_PROXY_URL
  AHA2_MINIMUM_FREE_GB
  AHA2_POWERSHELL
EOF
}

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
powershell="${AHA2_POWERSHELL:-/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe}"
version="${AHA2_VERSION:-0.5.22}"
install_dir="${AHA2_INSTALL_DIR:-D:\\Program Files\\AHA2}"
data_dir="${AHA2_DATA_DIR:-D:\\ProgramData\\AHA2}"
listen="${AHA2_LISTEN:-0.0.0.0:8766}"
health_url="${AHA2_HEALTH_URL:-http://127.0.0.1:8766/healthz}"
agent_api_url="${AHA2_AGENT_API_URL:-http://127.0.0.1:8766}"
result_path="${AHA2_RESULT_PATH:-$repo_dir/dist/user-deploy-result-local-build.json}"
installer_dir="${AHA2_INSTALLER_DIR:-}"
iscc_path="${AHA2_ISCC_PATH:-}"
minimum_free_gb="${AHA2_MINIMUM_FREE_GB:-3}"
proxy_url="${AHA2_PROXY_URL:-}"
mode="full"

while (($# > 0)); do
  case "$1" in
    --version)
      [[ $# -ge 2 ]] || { printf 'Missing value for --version\n' >&2; exit 2; }
      version="$2"
      shift 2
      ;;
    --validate-only)
      mode="validate"
      shift
      ;;
    --build-only)
      mode="build"
      shift
      ;;
    --deploy-only)
      mode="deploy"
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

[[ -x "$powershell" ]] || { printf 'Windows PowerShell was not found: %s\n' "$powershell" >&2; exit 1; }
command -v node >/dev/null 2>&1 || { printf 'node was not found\n' >&2; exit 1; }
command -v wslpath >/dev/null 2>&1 || { printf 'wslpath was not found\n' >&2; exit 1; }
[[ -x "$repo_dir/.tools/go/bin/go" ]] || { printf 'Repository Go toolchain was not found\n' >&2; exit 1; }
[[ -f "$repo_dir/go.mod" ]] || { printf 'AHA2 go.mod was not found\n' >&2; exit 1; }

available_kb="$(df -Pk "$repo_dir" | awk 'NR == 2 {print $4}')"
required_kb=$((minimum_free_gb * 1024 * 1024))
if [[ ! "$available_kb" =~ ^[0-9]+$ ]] || ((available_kb < required_kb)); then
  printf 'Repository drive has insufficient free space; at least %s GB is required\n' "$minimum_free_gb" >&2
  exit 1
fi

result_path="$(realpath -m "$result_path")"
case "$result_path" in
  "$repo_dir"/*) ;;
  *)
    printf 'AHA2_RESULT_PATH must stay inside the repository: %s\n' "$result_path" >&2
    exit 1
    ;;
esac
mkdir -p -- "$(dirname -- "$result_path")"

repo_win="$(wslpath -w "$repo_dir")"
build_deploy_win="$(wslpath -w "$repo_dir/scripts/build-and-deploy-windows-user.ps1")"
deploy_win="$(wslpath -w "$repo_dir/scripts/deploy-windows-user.ps1")"
installer_builder_win="$(wslpath -w "$repo_dir/scripts/build-windows-installer.ps1")"
result_win="$(wslpath -w "$result_path")"
installed_server="${install_dir}\\aha2.exe"
installed_tray="${install_dir}\\aha2-tray.exe"
normalized_version="${version#v}"
if [[ -z "$installer_dir" ]]; then
  installer_dir="$repo_dir/dist/installer-user-$normalized_version"
fi
installer_dir="$(realpath -m "$installer_dir")"
case "$installer_dir" in
  "$repo_dir"/*) ;;
  *)
    printf 'AHA2_INSTALLER_DIR must stay inside the repository: %s\n' "$installer_dir" >&2
    exit 1
    ;;
esac
mkdir -p -- "$installer_dir"
installer_win="$(wslpath -w "$installer_dir")"
installer_exe="$installer_dir/AHA2-Setup-User-x64.exe"
git_hash="$(git -C "$repo_dir" rev-parse --short=12 HEAD)"
dirty_suffix=""
if [[ -n "$(git -C "$repo_dir" status --porcelain)" ]]; then
  dirty_suffix=".dirty"
fi
web_version="v${normalized_version}.$(date -u +%Y%m%d).${git_hash}${dirty_suffix}"

go_bin="$repo_dir/.tools/go/bin/go"
export GOCACHE="${GOCACHE:-$repo_dir/.tools/gocache-linux}"
export GOPATH="${GOPATH:-$repo_dir/.tools/gopath-linux}"
mkdir -p -- "$GOCACHE" "$GOPATH" "$repo_dir/dist"
if [[ -n "$proxy_url" ]]; then
  export HTTP_PROXY="$proxy_url"
  export HTTPS_PROXY="$proxy_url"
fi

deploy_common=(
  -RepoPath "$repo_win"
  -InstallDir "$install_dir"
  -DataDir "$data_dir"
  -Version "$version"
  -Listen "$listen"
  -HealthURL "$health_url"
  -AgentAPIURL "$agent_api_url"
  -AllowInsecureAgentAPI
  -AllowCustomUserWritableInstallDir
  -UpdateExistingInstallInPlace
)

run_powershell_file() {
  local script="$1"
  shift
  "$powershell" -NoLogo -NoProfile -ExecutionPolicy Bypass -File "$script" "$@"
}

build_user_installer() {
  local args=(
    -RepoPath "$repo_win"
    -InputExe "$(wslpath -w "$repo_dir/dist/aha2-windows-amd64.exe")"
    -InputTrayExe "$(wslpath -w "$repo_dir/dist/aha2-tray-windows-amd64.exe")"
    -OutputDir "$installer_win"
    -Version "$version"
    -PerUser
  )
  if [[ -f "$repo_dir/dist/plugins/feishu/windows-amd64/aha2-channel-feishu.exe" &&
        -f "$repo_dir/dist/plugins/feishu/windows-amd64/plugin.json" ]]; then
    args+=(
      -InputFeishuPlugin "$(wslpath -w "$repo_dir/dist/plugins/feishu/windows-amd64/aha2-channel-feishu.exe")"
      -InputFeishuManifest "$(wslpath -w "$repo_dir/dist/plugins/feishu/windows-amd64/plugin.json")"
    )
  fi
  if [[ -n "$iscc_path" ]]; then
    args+=(-ISCCPath "$iscc_path")
  fi
  printf '==> Build Windows per-user installer\n'
  run_powershell_file "$installer_builder_win" "${args[@]}"
  assert_pe "$installer_exe"
  [[ -f "$installer_exe.sha256" ]] || {
    printf 'Installer checksum is missing: %s.sha256\n' "$installer_exe" >&2
    exit 1
  }
}

validate_target() {
  run_powershell_file "$deploy_win" \
    -InputExe "$installed_server" \
    -InputTrayExe "$installed_tray" \
    "${deploy_common[@]}" \
    -ValidateOnly
}

assert_pe() {
  local file="$1"
  [[ -f "$file" ]] || { printf 'Candidate is missing: %s\n' "$file" >&2; exit 1; }
  [[ "$(LC_ALL=C head -c 2 "$file")" == "MZ" ]] || {
    printf 'Candidate is not a Windows PE executable: %s\n' "$file" >&2
    exit 1
  }
}

build_candidates() {
  printf '==> Build Web assets\n'
  node "$repo_dir/scripts/build-web.mjs"
  printf '==> Test Web build\n'
  node --test "$repo_dir/web/tests/build.test.mjs"

  printf '==> Sync embedded Web assets\n'
  rm -rf -- "$repo_dir/internal/webassets/dist"
  mkdir -p -- "$repo_dir/internal/webassets/dist"
  cp -a -- "$repo_dir/web/dist/." "$repo_dir/internal/webassets/dist/"

  printf '==> Run Go tests\n'
  (cd "$repo_dir" && "$go_bin" test ./...)
  printf '==> Run go vet\n'
  (cd "$repo_dir" && "$go_bin" vet ./...)

  printf '==> Build Windows server\n'
  (
    cd "$repo_dir"
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 "$go_bin" build -trimpath \
      -ldflags="-s -w -X main.version=$normalized_version -X main.webVersion=$web_version" \
      -o dist/aha2-windows-amd64.exe ./cmd/aha
  )
  printf '==> Build Windows tray\n'
  (
    cd "$repo_dir"
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 "$go_bin" build -trimpath \
      -ldflags="-s -w -H windowsgui -X main.version=$normalized_version" \
      -o dist/aha2-tray-windows-amd64.exe ./cmd/aha-tray
  )

  if [[ -f "$repo_dir/plugins/feishu/go.mod" ]]; then
    printf '==> Test Feishu plugin\n'
    (cd "$repo_dir/plugins/feishu" && "$go_bin" test ./...)
    printf '==> Vet Feishu plugin\n'
    (cd "$repo_dir/plugins/feishu" && "$go_bin" vet ./...)
    printf '==> Build Feishu plugin\n'
    mkdir -p -- "$repo_dir/dist/plugins/feishu/windows-amd64"
    (
      cd "$repo_dir/plugins/feishu"
      CGO_ENABLED=0 GOOS=windows GOARCH=amd64 "$go_bin" build -trimpath \
        -ldflags="-s -w" \
        -o "$repo_dir/dist/plugins/feishu/windows-amd64/aha2-channel-feishu.exe" .
    )
    cp -- "$repo_dir/plugins/feishu/plugin.json" "$repo_dir/dist/plugins/feishu/windows-amd64/plugin.json"
    plugin_hash="$(sha256sum "$repo_dir/dist/plugins/feishu/windows-amd64/aha2-channel-feishu.exe" | awk '{print $1}')"
    node - "$repo_dir/dist/plugins/feishu/windows-amd64/plugin.json" "$plugin_hash" <<'NODE'
const fs = require("node:fs");
const [manifestPath, hash] = process.argv.slice(2);
const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
manifest.sha256 = hash;
fs.writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
NODE
  fi

  assert_pe "$repo_dir/dist/aha2-windows-amd64.exe"
  assert_pe "$repo_dir/dist/aha2-tray-windows-amd64.exe"
  if [[ -f "$repo_dir/dist/plugins/feishu/windows-amd64/aha2-channel-feishu.exe" ]]; then
    assert_pe "$repo_dir/dist/plugins/feishu/windows-amd64/aha2-channel-feishu.exe"
  fi
  build_user_installer
}

deploy_candidates() {
  rm -f -- "$result_path"
  run_powershell_file "$build_deploy_win" \
    -Version "$version" \
    -RepoPath "$repo_win" \
    -InstallDir "$install_dir" \
    -DataDir "$data_dir" \
    -Listen "$listen" \
    -HealthURL "$health_url" \
    -AgentAPIURL "$agent_api_url" \
    -ResultPath "$result_win" \
    -MinimumFreeGB "$minimum_free_gb" \
    -AllowInsecureAgentAPI \
    -AllowCustomUserWritableInstallDir \
    -UpdateExistingInstallInPlace \
    -DeployOnly
}

printf 'Repository: %s\n' "$repo_dir"
printf 'Version: %s\n' "$version"
printf 'Web version: %s\n' "$web_version"
printf 'Install dir: %s\n' "$install_dir"
printf 'Data dir: %s\n' "$data_dir"
printf 'Result: %s\n' "$result_path"
printf 'Installer: %s\n' "$installer_exe"

validate_target
case "$mode" in
  validate)
    exit 0
    ;;
  build)
    build_candidates
    exit 0
    ;;
  deploy)
    deploy_candidates
    ;;
  full)
    build_candidates
    deploy_candidates
    ;;
esac
