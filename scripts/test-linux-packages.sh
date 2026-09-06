#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "${script_dir}/.." && pwd)"
package_dir="${repo_dir}/packaging/linux"

assert_contains() {
    local file="$1"
    local text="$2"
    if ! grep -Fq -- "$text" "$file"; then
        echo "$file is missing required text: $text" >&2
        exit 1
    fi
}

bash -n "${script_dir}/build-linux-packages.sh"
for script in "${package_dir}/debian/postinst" "${package_dir}/debian/prerm" "${package_dir}/debian/postrm"; do
    sh -n "$script"
done

service_file="${package_dir}/aha2.service"
assert_contains "$service_file" "User=aha2"
assert_contains "$service_file" "Group=aha2"
assert_contains "$service_file" "EnvironmentFile=/etc/aha2/aha2.env"
assert_contains "$service_file" "ExecStart=/usr/bin/aha2 serve"
assert_contains "$service_file" "WorkingDirectory=/var/lib/aha2"
assert_contains "$service_file" "Restart=on-failure"

assert_contains "${package_dir}/debian/conffiles" "/etc/aha2/aha2.env"
assert_contains "${package_dir}/debian/postinst" "systemctl daemon-reload"
assert_contains "${package_dir}/debian/postinst" "systemctl enable aha2.service"
assert_contains "${package_dir}/debian/postinst" "systemctl restart aha2.service"
assert_contains "${package_dir}/debian/postinst" "systemctl start aha2.service"
assert_contains "${package_dir}/debian/prerm" "systemctl stop aha2.service"
assert_contains "${package_dir}/debian/prerm" "systemctl disable aha2.service"

rpm_spec="${package_dir}/rpm/aha2.spec.in"
assert_contains "$rpm_spec" "%config(noreplace) /etc/aha2/aha2.env"
assert_contains "$rpm_spec" "groupadd --system aha2"
assert_contains "$rpm_spec" "useradd --system --gid aha2"
assert_contains "$rpm_spec" "systemctl daemon-reload"
assert_contains "$rpm_spec" "systemctl enable aha2.service"
assert_contains "$rpm_spec" "systemctl restart aha2.service"
assert_contains "$rpm_spec" "systemctl stop aha2.service"
assert_contains "$rpm_spec" "systemctl disable aha2.service"
assert_contains "${script_dir}/build-linux-packages.sh" 'rpmbuild -bb --target "$rpm_arch"'

if grep -R -E 'rm[[:space:]].*/var/lib/aha2' "${package_dir}" >/dev/null; then
    echo "packaging scripts must not delete /var/lib/aha2" >&2
    exit 1
fi

work_dir="$(mktemp -d)"
cleanup() {
    rm -rf -- "$work_dir"
}
trap cleanup EXIT
printf '#!/bin/sh\nexit 0\n' > "${work_dir}/aha2"
chmod 0755 "${work_dir}/aha2"

amd64_output="$(bash "${script_dir}/build-linux-packages.sh" --version 1.2.3 --arch amd64 --input-exe "${work_dir}/aha2" --output-dir "${work_dir}/out-amd64" --validate-only 2>&1)"
grep -Fq "aha2_1.2.3_amd64.deb" <<<"$amd64_output"
grep -Fq "aha2-1.2.3-1.x86_64.rpm" <<<"$amd64_output"

arm64_output="$(bash "${script_dir}/build-linux-packages.sh" -Version v1.2.3 -Arch arm64 -InputExe "${work_dir}/aha2" -OutputDir "${work_dir}/out-arm64" -ValidateOnly 2>&1)"
grep -Fq "aha2_1.2.3_arm64.deb" <<<"$arm64_output"
grep -Fq "aha2-1.2.3-1.aarch64.rpm" <<<"$arm64_output"

echo "Linux package templates and build arguments are valid."
