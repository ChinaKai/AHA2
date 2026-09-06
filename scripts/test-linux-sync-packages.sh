#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "${script_dir}/.." && pwd)"
package_dir="${repo_dir}/packaging/linux-sync"

assert_contains() {
    local file="$1"
    local text="$2"
    if ! grep -Fq -- "$text" "$file"; then
        echo "$file is missing required text: $text" >&2
        exit 1
    fi
}

bash -n "${script_dir}/build-linux-sync-packages.sh"
for script in "${package_dir}/debian/postinst" "${package_dir}/debian/prerm" "${package_dir}/debian/postrm"; do
    sh -n "$script"
done

service_file="${package_dir}/aha2-sync.service"
assert_contains "$service_file" "User=aha2-sync"
assert_contains "$service_file" "Group=aha2-sync"
assert_contains "$service_file" "EnvironmentFile=/etc/aha2-sync/aha2-sync.env"
assert_contains "$service_file" "ExecStart=/usr/bin/aha-sync"
assert_contains "$service_file" "WorkingDirectory=/var/lib/aha2-sync"
assert_contains "$service_file" "Restart=on-failure"

assert_contains "${package_dir}/aha2-sync.env" "AHA2_SYNC_LISTEN=127.0.0.1:8770"
assert_contains "${package_dir}/aha2-sync.env" "AHA2_SYNC_DB=/var/lib/aha2-sync/sync.db"
assert_contains "${package_dir}/debian/conffiles" "/etc/aha2-sync/aha2-sync.env"
assert_contains "${package_dir}/debian/postinst" "systemctl enable aha2-sync.service"
assert_contains "${package_dir}/debian/postinst" "systemctl restart aha2-sync.service"
assert_contains "${package_dir}/debian/prerm" "systemctl stop aha2-sync.service"
assert_contains "${package_dir}/debian/prerm" "systemctl disable aha2-sync.service"

rpm_spec="${package_dir}/rpm/aha2-sync.spec.in"
assert_contains "$rpm_spec" "%config(noreplace) /etc/aha2-sync/aha2-sync.env"
assert_contains "$rpm_spec" "groupadd --system aha2-sync"
assert_contains "$rpm_spec" "useradd --system --gid aha2-sync"
assert_contains "$rpm_spec" "systemctl enable aha2-sync.service"
assert_contains "$rpm_spec" "systemctl restart aha2-sync.service"
assert_contains "$rpm_spec" "systemctl stop aha2-sync.service"
assert_contains "$rpm_spec" "systemctl disable aha2-sync.service"
assert_contains "${script_dir}/build-linux-sync-packages.sh" 'rpmbuild -bb --target "$rpm_arch"'

if grep -R -E 'rm[[:space:]].*/var/lib/aha2-sync' "${package_dir}" >/dev/null; then
    echo "packaging scripts must not delete /var/lib/aha2-sync" >&2
    exit 1
fi

work_dir="$(mktemp -d)"
cleanup() {
    rm -rf -- "$work_dir"
}
trap cleanup EXIT
printf '#!/bin/sh\nexit 0\n' > "${work_dir}/aha-sync"
chmod 0755 "${work_dir}/aha-sync"

amd64_output="$(bash "${script_dir}/build-linux-sync-packages.sh" --version 1.2.3 --arch amd64 --input-exe "${work_dir}/aha-sync" --output-dir "${work_dir}/out-amd64" --validate-only 2>&1)"
grep -Fq "aha2-sync_1.2.3_amd64.deb" <<<"$amd64_output"
grep -Fq "aha2-sync-1.2.3-1.x86_64.rpm" <<<"$amd64_output"

arm64_output="$(bash "${script_dir}/build-linux-sync-packages.sh" -Version v1.2.3 -Arch arm64 -InputExe "${work_dir}/aha-sync" -OutputDir "${work_dir}/out-arm64" -ValidateOnly 2>&1)"
grep -Fq "aha2-sync_1.2.3_arm64.deb" <<<"$arm64_output"
grep -Fq "aha2-sync-1.2.3-1.aarch64.rpm" <<<"$arm64_output"

echo "AHA Sync Center Linux package templates and build arguments are valid."
