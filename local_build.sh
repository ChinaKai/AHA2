#!/usr/bin/env bash
set -Eeuo pipefail

# Convenience entry for building the Windows release package. The repository
# only builds; installation, stopping, backup, replacement, rollback and health
# checks belong to the installer on the target machine, so there is no deploy
# mode here.
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
exec bash "$repo_dir/scripts/build-windows-package.sh" "$@"
