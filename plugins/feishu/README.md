# AHA2 Feishu channel plugin

This optional process hosts the Feishu/Lark SDK. AHA2 starts one process per channel instance and passes its bootstrap capability and App Secret only through an inherited anonymous pipe. The process calls back to `/api/channel-runtime/v1`; it does not listen on a network port.

Build a package with:

```bash
./scripts/build-feishu-plugin.sh windows/amd64
```

The build script writes the executable and a manifest containing its SHA-256 to `dist/plugins/feishu/<os>-<arch>/`. Installation copies those two files into the AHA2 data directory under `plugins/channels/feishu/`. The core remains buildable and usable when this nested Go module or its binary is absent.

The plugin uses `github.com/larksuite/oapi-sdk-go/v3` v3.9.10 for Device Authorization app registration, WebSocket events, message normalization, and cards. Outbound create/reply calls use the lower-level IM API so AHA2's durable delivery key is mapped to Feishu's one-hour UUID deduplication field.
