#!/bin/sh
set -eu

if [ "$(/usr/bin/id -u)" -ne 0 ]; then
  echo "Run this uninstaller as an administrator: sudo $0" >&2
  exit 1
fi

label="com.aha2.controlplane"
plist="/Library/LaunchDaemons/${label}.plist"
data_dir="/Library/Application Support/AHA2"

/bin/launchctl bootout "system/${label}" >/dev/null 2>&1 || \
  /bin/launchctl bootout system "${plist}" >/dev/null 2>&1 || true
/bin/rm -f /usr/local/bin/aha2 "${plist}"
/usr/sbin/pkgutil --forget "${label}" >/dev/null 2>&1 || true
/bin/rm -rf /usr/local/share/aha2

echo "AHA2 was uninstalled. Persistent data was retained at: ${data_dir}"
