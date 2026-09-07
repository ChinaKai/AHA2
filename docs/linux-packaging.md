# Linux packages

AHA2 provides native package layouts for Debian/Ubuntu and Fedora/RHEL. Both formats install the same systemd service contract:

- executable: `/usr/bin/aha2`
- service: `aha2.service`
- dedicated system account: `aha2:aha2`
- configuration: `/etc/aha2/aha2.env`
- persistent data: `/var/lib/aha2`
- listener: `127.0.0.1:8766`
- restart policy: `on-failure`

The service starts `/usr/bin/aha2 serve`. Package installation enables and starts it; an upgrade reloads systemd and restarts a running service. Package removal stops and disables the service but intentionally retains `/var/lib/aha2` and the `aha2` system account.

## Build packages

Build a Linux binary for each target architecture before packaging. For example:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/aha2-linux-amd64 ./cmd/aha
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o dist/aha2-linux-arm64 ./cmd/aha
```

Then invoke the package builder once per architecture:

```bash
bash scripts/build-linux-packages.sh \
  --version 0.2.1 \
  --arch amd64 \
  --input-exe dist/aha2-linux-amd64 \
  --output-dir dist/linux

bash scripts/build-linux-packages.sh \
  --version 0.2.1 \
  --arch arm64 \
  --input-exe dist/aha2-linux-arm64 \
  --output-dir dist/linux
```

Stable output names are:

- `aha2_<version>_amd64.deb`
- `aha2_<version>_arm64.deb`
- `aha2-<version>-1.x86_64.rpm`
- `aha2-<version>-1.aarch64.rpm`

`dpkg-deb` is required for `.deb` output. When `rpmbuild` is available, the same invocation also produces the matching `.rpm`; otherwise RPM creation is skipped with a clear message. Use `--validate-only` to validate both package definitions and planned filenames without invoking either packager:

```bash
bash scripts/build-linux-packages.sh --version 0.2.1 --arch amd64 \
  --input-exe dist/aha2-linux-amd64 --output-dir dist/linux --validate-only
bash scripts/test-linux-packages.sh
```

## Install and configure

Install a Debian package with `sudo apt install ./aha2_<version>_amd64.deb`, or an RPM with `sudo dnf install ./aha2-<version>-1.x86_64.rpm`. The service can then be inspected with:

```bash
systemctl status aha2.service
curl http://127.0.0.1:8766/healthz
```

首次安装后读取 Owner Setup Token；创建 Owner 后，该文件继续用于忘记密码时的本机恢复：

```bash
sudo cat /var/lib/aha2/setup-token
```

Edit `/etc/aha2/aha2.env` to change runtime settings, then run `sudo systemctl restart aha2.service`. Debian treats this file as a conffile; RPM marks it `%config(noreplace)`, so package upgrades preserve administrator changes.

要允许局域网访问，可将 `AHA2_LISTEN` 改为 `0.0.0.0:<端口>` 或指定网卡 IP；防火墙规则由管理员按发行版安全策略单独配置。

Uninstall with the distribution package manager. Back up `/var/lib/aha2` before intentionally removing application data; uninstall scripts never delete that directory.
