# AHA2

[中文](README.md) | English

AHA2 is a personal AI workflow system that uses local projects as its boundary and tasks as its unit of execution. As the control plane, it orchestrates projects, tasks, permissions, approvals, and knowledge. The actual coding and reasoning are handed to agent harnesses such as Codex, Claude, and DeepSeek Harness, which run in isolated workspaces on the local machine, in WSL, or on remote hosts over SSH.

A task is more than a prompt. Each task can attach the callable resources it needs: a built-in browser, a shared desktop, email sending, and hardware terminals over serial, TCP/Telnet, SSH, and ADB. Physical devices and software systems are exposed to the harness as tools, so the agent can actually integrate, verify, and operate, not just produce text.

All of these resources are presented in the Web UI within a single task context. The owner can follow a task's progress while taking over the browser or shared desktop, handling approvals, and providing input; email and hardware devices are viewed and managed in the same place. Once access is configured, the owner can step in from anywhere that can open the AHA2 Web UI, without sitting at the machine running the task. Tasks can also be queried, confirmed, and taken over from direct messages in channels such as Feishu (Lark).

AHA2 also includes a resident pet that acts as the owner's long-term partner. It runs continuously on the server, keeping an eye on task progress for the owner, creating and following up on tasks, requesting independent code reviews, and organizing knowledge and configuration. It uses resources such as the browser, hardware, and email only within the permissions the owner has granted, and asks the owner for approval before permission changes or sensitive operations. It also keeps its own work log and has a personality that grows over time, so it is more than a command entry point.

Through its knowledge loop, AHA2 carries verified lessons from each task into later ones. Knowledge is stored as Markdown files with sources and evidence, building up over time into a lasting asset of the project.

```text
Project → Workspace → Task → Turn → Task Memory → Knowledge → Next Turn / Task
```

## Installation

Download the package for your platform from [GitHub Releases](https://github.com/ChinaKai/AHA2/releases), and verify it with the `SHA256SUMS` file from the same release.

### Windows

Run `AHA2-Setup-User-x64.exe`. This is the only Windows installer. It installs per user and never requests UAC:
the program goes into the current user's `%LOCALAPPDATA%\Programs\AHA2`, the data directory defaults to `%LOCALAPPDATA%\AHA2`,
and by default it listens only on `127.0.0.1:8766`. The package installs both the `aha2.exe` control plane and `aha2-tray.exe`, which uses the GUI subsystem.
The setup wizard lets you choose the data directory, listen IP, and port, but it no longer creates firewall rules. If you need LAN access, open it according to your own network policy.
A single `AHA2 User` logon scheduled task starts the tray program directly with the logged-on user's least privileges, and the tray manages the control plane process,
so it inherits that user's Git, Codex, Claude, WSL, and SSH environment. The installer does not create a Windows service, and it does not handle the legacy
LocalSystem service.

Earlier versions shipped a machine-wide installer, `AHA2-Setup-x64.exe` (installed to `Program Files\AHA2`, required UAC,
and could create firewall rules and a service). It is no longer built or published. If it is installed on a machine, first uninstall the old machine-wide
installation with administrator rights (the old uninstaller stops AHA2 and deletes the `AHA2 User` logon task), then run the per-user installer, and
manually select the old data directory in the wizard; otherwise the data directory defaults to `%LOCALAPPDATA%\AHA2`. After installing, confirm that the logon task points to the per-user
program and that the new instance is healthy. Do not install the per-user version while the old machine-wide installation is still present: the per-user installer keeps an existing old logon
task, so the installation appears to succeed while the old program is what actually runs.

The tray writes the stdout/stderr of `aha2.exe serve` to `<data directory>\logs\aha2-server.log`, and records the startup command line, PID, exit code, or startup error. The file is capped at 10 MB; beyond that it rotates to `aha2-server.log.1` (at most two files are kept). Check this file first when troubleshooting startup failures or crashes.

To produce the Windows package on a build machine (both exes, the Feishu plugin directory, `browser-host/`, a pinned
`node/node.exe` with `node/LICENSE`, and `release-manifest.json`):

```bash
./local_build.sh --version 0.5.23
# equivalent to
bash scripts/build-windows-package.sh --version 0.5.23 --output-dir dist
```

The repository only builds. It does not stop services, replace files, or roll back; installation and upgrades are handled by the native installer on the target machine.
`release-manifest.json` records the version, commit, build time, generation method, and the SHA256 of every artifact. The installer installs
it unchanged into the program directory, and deployment scripts use it to verify the installed result file by file. The installer build cross-checks the manifest against the inputs it will
copy, in both directions, and checks that the version, commit, and Web version agree; if they do not, no installer is produced.
The release also provides `AHA2-Windows-Package-x64.zip`, which contains the official PackageDir layout (the `dist/` package and
`installer/AHA2-Setup-User-x64.exe`). Windows has only this one installer name.

### Debian / Ubuntu

```bash
sudo apt install ./aha2_<version>_amd64.deb
# For ARM64, use aha2_<version>_arm64.deb
```

### Fedora / RHEL

```bash
sudo dnf install ./aha2-<version>-1.x86_64.rpm
# For ARM64, use aha2-<version>-1.aarch64.rpm
```

On Linux, systemd starts AHA2 automatically after installation. The listen address and data directory can be changed in `/etc/aha2/aha2.env`.

### macOS

Run `AHA2-macos-amd64.pkg` or `AHA2-macos-arm64.pkg` according to your machine's architecture. Installation requires administrator rights; afterwards the launchd service is registered and started automatically.

### First-time setup

Open `http://127.0.0.1:8766` (the default) and create the Owner using the `setup-token` in the data directory chosen during installation. The same file is also used for local recovery if you later forget the password. This is the **data directory, not the program installation directory**. If you replace or restore this file, restart AHA2 so it loads the new value:

- Windows: the data directory chosen during installation, by default `%LOCALAPPDATA%\AHA2\setup-token`
- Linux: `sudo cat /var/lib/aha2/setup-token`
- macOS: `sudo cat "/Library/Application Support/AHA2/setup-token"`

For full installation, upgrade, and uninstall instructions, see the [deployment guide](docs/deployment.md) (Chinese).

## Settings sync

First install the standalone Sync Center on a Linux server (do not confuse it with the AHA2 client package installed on each device):

```bash
sudo apt install ./aha2-sync_<version>_amd64.deb
# Fedora/RHEL: sudo dnf install ./aha2-sync-<version>-1.x86_64.rpm
```

The service listens on `127.0.0.1:8770` by default. Once it is exposed through an HTTPS reverse proxy, generate a one-time registration code valid for 24 hours:

```bash
sudo -u aha2-sync /usr/bin/aha-sync --db /var/lib/aha2-sync/sync.db \
  --generate-registration-code /var/lib/aha2-sync/registration-code
sudo cat /var/lib/aha2-sync/registration-code
sudo rm /var/lib/aha2-sync/registration-code
```

Then, on each AHA2 device:

1. Open the "Sync" page in the Web UI and enable sync.
2. Enter the HTTPS center address, device name, sync interval, and one-time registration code.
3. Set an encryption passphrase of at least 12 characters; every other device must use the same passphrase.
4. Save the settings and click "Sync now", then check pushes, deletions, and conflicts on the same page.

Projects, tasks, the knowledge base, models, and Agent configuration are synced in dependency order, and deletions propagate as well. Provider, Env Secret, Codex, SSH, and hardware credentials are end-to-end encrypted with the shared passphrase. SSH and hardware entries are only read-only remote mirrors; taking one over to the local machine requires reconfirming the path, serial port, or network endpoint. Each device's Sync Token is always separate and is never synced.

For center configuration, upgrade, uninstall, and reverse proxy instructions, see [Sync Center deployment](docs/sync-center.md) (Chinese).

## Task worktrees and manual cleanup

When a worktree-isolated Task finishes, wrap-up first merges the task branch back into the target branch, then checks that the worktree is clean, that HEAD has been merged into the target branch, that it is not locked by git, and that it contains no submodules. Only when all of these hold is the worktree deleted, and `--force` is never used. On Windows, paths longer than 260 characters are deleted using the `\\?\` extended-length path form, so callers do not need to clean up manually.

The wrap-up result reports the merge method and commit information: `merge_strategy` is `fast_forward`, `merge_commit`, or `none`, along with `source_head` (the task branch HEAD before merging), `target_head_before` / `target_head_after` (the target branch HEAD before and after merging), and `commits`. For a fast-forward merge, `target_head_after == source_head`; for a regular merge, `target_head_after` is the newly created merge commit; when there is nothing to merge, the strategy is `none` and `target_head_before == target_head_after`. If the worktree is dirty, the original workspace is dirty, or the merge conflicts, the function returns an error and keeps the worktree and branch, and the result does not fill in a `target_head_after` that never happened. The audit record contains only the merge strategy, commit count, and these SHAs, with no credentials or paths.

When older versions logged `Filename too long` during wrap-up, they could leave behind an undeleted `.aha2-worktrees/<task>` directory. AHA2 does not clean up these historical leftovers automatically. You can delete such a directory manually once you have confirmed that:

- `git worktree list` no longer lists it (the worktree metadata has been removed);
- `git -C <directory> status --porcelain` is empty, or the directory no longer holds a usable checkout;
- every commit at the directory's HEAD has been merged into the target branch (`git -C <repository> merge-base --is-ancestor <task-head> <target>`);
- it is not locked by git and contains no submodules with unpushed commits.

If in doubt, keep the directory: it only takes up disk space and does not affect correctness.

## Documentation

The documents below are written in Chinese.

- [Core design](docs/core-design.md)
- [Deployment](docs/deployment.md)
- [Linux packaging and configuration](docs/linux-packaging.md)
- [Sync Center deployment](docs/sync-center.md)
- [Agent API](docs/agent-api.md)
- [Testing and quick verification](docs/testing.md)
- [File knowledge vault](docs/knowledge-vault.md)
- [Offline database compaction](docs/offline-database-compaction.md)
- [AHA1 knowledge import](docs/aha1-knowledge-import.md)
