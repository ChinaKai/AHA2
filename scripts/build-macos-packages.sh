#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="0.0.0"
input_exe=""
arch=""
output_dir="$repo/dist"
signing_identity=""
validate_only=false

usage() {
  cat <<'EOF'
Usage: build-macos-packages.sh --version VERSION --input-exe PATH --arch amd64|arm64 [options]

Options:
  --output-dir PATH                 Output directory (default: dist)
  --signing-identity NAME           Optional Developer ID Installer identity
  --validate-only                   Validate contracts without running macOS package tools
EOF
}

while (($#)); do
  case "$1" in
    --version|-Version) version="${2:?missing Version}"; shift 2 ;;
    --input-exe|-InputExe) input_exe="${2:?missing InputExe}"; shift 2 ;;
    --arch|-Arch) arch="${2:?missing Arch}"; shift 2 ;;
    --output-dir|-OutputDir) output_dir="${2:?missing OutputDir}"; shift 2 ;;
    --signing-identity|--developer-id-installer|-SigningIdentity|-DeveloperIDInstaller) signing_identity="${2:?missing signing identity}"; shift 2 ;;
    --validate-only|-ValidateOnly) validate_only=true; shift ;;
    --help|-h) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

case "$arch" in
  amd64|x86_64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "Arch must be amd64 or arm64." >&2; exit 2 ;;
esac

package_version="${version#v}"
if [[ ! "$package_version" =~ ^[0-9]+([.][0-9]+){0,3}([._-][0-9A-Za-z]+)?$ ]]; then
  echo "Version is not valid for a macOS package: $version" >&2
  exit 2
fi

packaging="$repo/packaging/macos"
plist="$packaging/com.aha2.controlplane.plist"
preinstall="$packaging/scripts/preinstall"
postinstall="$packaging/scripts/postinstall"
uninstaller="$packaging/uninstall-aha2.sh"
for required in "$plist" "$preinstall" "$postinstall" "$uninstaller"; do
  [[ -f "$required" ]] || { echo "Missing packaging file: $required" >&2; exit 1; }
done

grep -Fq '<string>/usr/local/bin/aha2</string>' "$plist"
grep -Fq '<string>serve</string>' "$plist"
grep -Fq '<string>127.0.0.1:8766</string>' "$plist"
grep -Fq '<string>/Library/Application Support/AHA2</string>' "$plist"
grep -Fq '<key>RunAtLoad</key>' "$plist"
grep -Fq '<key>KeepAlive</key>' "$plist"
grep -Fq '<key>StandardErrorPath</key>' "$plist"
grep -Fq 'launchctl bootout' "$preinstall"
grep -Fq 'launchctl bootstrap' "$postinstall"
grep -Fq 'launchctl kickstart' "$postinstall"

if $validate_only; then
  if [[ -n "$input_exe" && -f "$input_exe" ]]; then
    echo "macOS package contracts valid; input executable found for $arch."
  else
    echo "macOS package contracts valid; input executable is not present, so pkgbuild was not run."
  fi
  exit 0
fi

[[ "$(uname -s)" == "Darwin" ]] || { echo "pkgbuild requires macOS. Use --validate-only on other systems." >&2; exit 1; }
[[ -n "$input_exe" && -f "$input_exe" ]] || { echo "Input executable not found: $input_exe" >&2; exit 1; }
command -v pkgbuild >/dev/null || { echo "pkgbuild was not found." >&2; exit 1; }
command -v plutil >/dev/null || { echo "plutil was not found." >&2; exit 1; }
command -v file >/dev/null || { echo "file was not found." >&2; exit 1; }

file_description="$(file -b "$input_exe")"
case "$arch" in
  amd64) [[ "$file_description" == *x86_64* || "$file_description" == *x86-64* ]] || { echo "Input executable is not macOS amd64." >&2; exit 1; } ;;
  arm64) [[ "$file_description" == *arm64* || "$file_description" == *aarch64* ]] || { echo "Input executable is not macOS arm64." >&2; exit 1; } ;;
esac

stage="$(mktemp -d "${TMPDIR:-/tmp}/aha2-pkg.XXXXXX")"
trap 'rm -rf -- "$stage"' EXIT
root="$stage/root"
package_scripts="$stage/scripts"
/bin/mkdir -p "$root/usr/local/bin" "$root/usr/local/share/aha2" \
  "$root/Library/LaunchDaemons" "$root/Library/Application Support/AHA2" "$package_scripts" "$output_dir"
/bin/cp "$input_exe" "$root/usr/local/bin/aha2"
/bin/cp "$plist" "$root/Library/LaunchDaemons/com.aha2.controlplane.plist"
/bin/cp "$uninstaller" "$root/usr/local/share/aha2/uninstall.sh"
/bin/cp "$preinstall" "$postinstall" "$package_scripts/"
/bin/chmod 0755 "$root/usr/local/bin/aha2" "$root/usr/local/share/aha2/uninstall.sh" "$package_scripts/preinstall" "$package_scripts/postinstall"
/bin/chmod 0644 "$root/Library/LaunchDaemons/com.aha2.controlplane.plist"
plutil -lint "$root/Library/LaunchDaemons/com.aha2.controlplane.plist" >/dev/null

output="$output_dir/AHA2-macos-${arch}.pkg"
pkg_args=(--root "$root" --scripts "$package_scripts" --identifier com.aha2.controlplane \
  --version "$package_version" --install-location / --ownership recommended)
if [[ -n "$signing_identity" ]]; then
  pkg_args+=(--sign "$signing_identity")
fi
pkgbuild "${pkg_args[@]}" "$output"
echo "macOS package: $output"
