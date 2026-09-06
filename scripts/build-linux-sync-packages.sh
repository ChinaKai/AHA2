#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "${script_dir}/.." && pwd)"
package_dir="${repo_dir}/packaging/linux-sync"

version="${Version:-}"
arch="${Arch:-}"
input_exe="${InputExe:-}"
output_dir="${OutputDir:-${repo_dir}/dist/linux-sync}"
validate_only="${ValidateOnly:-false}"

usage() {
    cat <<'EOF'
Usage: scripts/build-linux-sync-packages.sh --version VERSION --arch amd64|arm64 \
       --input-exe PATH [--output-dir PATH] [--validate-only]

PowerShell-style names (-Version, -Arch, -InputExe, -OutputDir, -ValidateOnly)
and matching environment variables are also accepted.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version|-Version)
            version="${2:-}"
            shift 2
            ;;
        --arch|-Arch)
            arch="${2:-}"
            shift 2
            ;;
        --input-exe|-InputExe)
            input_exe="${2:-}"
            shift 2
            ;;
        --output-dir|-OutputDir)
            output_dir="${2:-}"
            shift 2
            ;;
        --validate-only|-ValidateOnly)
            validate_only=true
            shift
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            echo "unknown argument: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if [[ "$version" =~ ^[vV][0-9] ]]; then
    version="${version:1}"
fi
if [[ ! "$version" =~ ^[0-9][0-9A-Za-z._+]*$ ]]; then
    echo "Version must start with a digit and contain only letters, digits, dot, underscore, or plus." >&2
    exit 2
fi

case "$arch" in
    amd64|x86_64)
        deb_arch=amd64
        rpm_arch=x86_64
        ;;
    arm64|aarch64)
        deb_arch=arm64
        rpm_arch=aarch64
        ;;
    *)
        echo "Arch must be amd64, arm64, x86_64, or aarch64." >&2
        exit 2
        ;;
esac

if [[ -z "$input_exe" || ! -f "$input_exe" ]]; then
    echo "InputExe must name an existing AHA Sync Center Linux binary." >&2
    exit 2
fi

required_files=(
    "${package_dir}/aha2-sync.service"
    "${package_dir}/aha2-sync.env"
    "${package_dir}/debian/control.in"
    "${package_dir}/debian/conffiles"
    "${package_dir}/debian/postinst"
    "${package_dir}/debian/prerm"
    "${package_dir}/debian/postrm"
    "${package_dir}/rpm/aha2-sync.spec.in"
)
for required in "${required_files[@]}"; do
    if [[ ! -f "$required" ]]; then
        echo "missing packaging file: $required" >&2
        exit 1
    fi
done

grep -Fq 'User=aha2-sync' "${package_dir}/aha2-sync.service"
grep -Fq 'Group=aha2-sync' "${package_dir}/aha2-sync.service"
grep -Fq 'EnvironmentFile=/etc/aha2-sync/aha2-sync.env' "${package_dir}/aha2-sync.service"
grep -Fq 'ExecStart=/usr/bin/aha-sync' "${package_dir}/aha2-sync.service"
grep -Fq 'Restart=on-failure' "${package_dir}/aha2-sync.service"
grep -Fxq 'AHA2_SYNC_LISTEN=127.0.0.1:8770' "${package_dir}/aha2-sync.env"
grep -Fxq 'AHA2_SYNC_DB=/var/lib/aha2-sync/sync.db' "${package_dir}/aha2-sync.env"
grep -Fxq '/etc/aha2-sync/aha2-sync.env' "${package_dir}/debian/conffiles"
grep -Fq '%config(noreplace) /etc/aha2-sync/aha2-sync.env' "${package_dir}/rpm/aha2-sync.spec.in"

mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd)"
input_exe="$(cd "$(dirname "$input_exe")" && pwd)/$(basename "$input_exe")"
deb_output="${output_dir}/aha2-sync_${version}_${deb_arch}.deb"
rpm_output="${output_dir}/aha2-sync-${version}-1.${rpm_arch}.rpm"

echo "validated AHA Sync Center package inputs: version=${version} deb_arch=${deb_arch} rpm_arch=${rpm_arch}"
echo "DEB output: ${deb_output}"
echo "RPM output: ${rpm_output}"

if [[ "$validate_only" == "true" || "$validate_only" == "1" ]]; then
    if ! command -v rpmbuild >/dev/null 2>&1; then
        echo "RPM template validated; rpmbuild is not installed, so no RPM will be produced." >&2
    fi
    exit 0
fi

if command -v file >/dev/null 2>&1; then
    binary_info="$(file -b "$input_exe")"
    case "$deb_arch" in
        amd64)
            if [[ "$binary_info" != *"ELF 64-bit"* || "$binary_info" != *"x86-64"* ]]; then
                echo "InputExe is not an amd64 ELF binary: $binary_info" >&2
                exit 2
            fi
            ;;
        arm64)
            if [[ "$binary_info" != *"ELF 64-bit"* || "$binary_info" != *"ARM aarch64"* ]]; then
                echo "InputExe is not an arm64 ELF binary: $binary_info" >&2
                exit 2
            fi
            ;;
    esac
fi

if ! command -v dpkg-deb >/dev/null 2>&1; then
    echo "dpkg-deb is required to build the Debian package." >&2
    exit 1
fi

work_dir="$(mktemp -d)"
cleanup() {
    rm -rf -- "$work_dir"
}
trap cleanup EXIT
rm -f -- "$deb_output" "$rpm_output"

deb_root="${work_dir}/deb"
install -d "${deb_root}/DEBIAN" "${deb_root}/usr/bin" "${deb_root}/etc/aha2-sync" "${deb_root}/lib/systemd/system"
install -m 0755 "$input_exe" "${deb_root}/usr/bin/aha-sync"
install -m 0644 "${package_dir}/aha2-sync.env" "${deb_root}/etc/aha2-sync/aha2-sync.env"
install -m 0644 "${package_dir}/aha2-sync.service" "${deb_root}/lib/systemd/system/aha2-sync.service"
sed -e "s/@VERSION@/${version}/g" -e "s/@ARCH@/${deb_arch}/g" "${package_dir}/debian/control.in" > "${deb_root}/DEBIAN/control"
install -m 0644 "${package_dir}/debian/conffiles" "${deb_root}/DEBIAN/conffiles"
for maintainer_script in postinst prerm postrm; do
    install -m 0755 "${package_dir}/debian/${maintainer_script}" "${deb_root}/DEBIAN/${maintainer_script}"
done
dpkg-deb --root-owner-group --build "$deb_root" "$deb_output"

if command -v rpmbuild >/dev/null 2>&1; then
    rpm_root="${work_dir}/rpm"
    install -d "${rpm_root}/BUILD" "${rpm_root}/BUILDROOT" "${rpm_root}/RPMS" "${rpm_root}/SOURCES" "${rpm_root}/SPECS" "${rpm_root}/SRPMS"
    install -m 0755 "$input_exe" "${rpm_root}/SOURCES/aha-sync"
    install -m 0644 "${package_dir}/aha2-sync.service" "${rpm_root}/SOURCES/aha2-sync.service"
    install -m 0644 "${package_dir}/aha2-sync.env" "${rpm_root}/SOURCES/aha2-sync.env"
    sed -e "s/@VERSION@/${version}/g" -e "s/@RPM_ARCH@/${rpm_arch}/g" "${package_dir}/rpm/aha2-sync.spec.in" > "${rpm_root}/SPECS/aha2-sync.spec"
    rpmbuild -bb --target "$rpm_arch" --define "_topdir ${rpm_root}" "${rpm_root}/SPECS/aha2-sync.spec"
    built_rpm="${rpm_root}/RPMS/${rpm_arch}/aha2-sync-${version}-1.${rpm_arch}.rpm"
    if [[ ! -f "$built_rpm" ]]; then
        echo "rpmbuild completed without the expected artifact: $built_rpm" >&2
        exit 1
    fi
    install -m 0644 "$built_rpm" "$rpm_output"
else
    echo "RPM build skipped: install rpmbuild and rerun; --validate-only already covers template validation." >&2
fi

sha256sum "$deb_output"
if [[ -f "$rpm_output" ]]; then
    sha256sum "$rpm_output"
fi
