# AHA2 Feishu channel plugin

This optional process hosts the Feishu/Lark SDK. AHA2 starts one process per channel instance and passes its bootstrap capability and App Secret only through an inherited anonymous pipe. The process calls back to `/api/channel-runtime/v1`; it does not listen on a network port.

Build a package with:

```bash
./scripts/build-feishu-plugin.sh windows/amd64
```

The build script writes the executable and a manifest containing its SHA-256 to `dist/plugins/feishu/<os>-<arch>/`. Installation copies those two files into the AHA2 data directory under `plugins/channels/feishu/`. The core remains buildable and usable when this nested Go module or its binary is absent.

The plugin uses `github.com/larksuite/oapi-sdk-go/v3` v3.9.10 for Device Authorization app registration, WebSocket events, message normalization, and cards. Outbound create/reply calls use the lower-level IM API so AHA2's durable delivery key is mapped to Feishu's one-hour UUID deduplication field.

## Images and files

### Permission-only reauthorization (1.1.1)

Existing-app QR authorization uses the SDK's incremental scope Addons and targets the same App ID. It does not send creation presets, names, events, callbacks, bot abilities, menus, or an AHA-initiated publish request. The server still enforces the same unique Owner. Permission approval and any necessary publication remain on the official confirmation/developer page; review all outstanding draft changes before publishing.

Process startup and credential rotation no longer enqueue menu configuration. Queued legacy `configure_menu` commands complete as skipped without provider requests, including retries. New applications alone receive a transactional, onboarding-scoped `initialize_menu` command. The creation dialog explicitly discloses that initialization submits a release. Initialization is attempted only once within five minutes, against the newly created App ID; reauthorization cancels outstanding initialization. Rejected menus do not fall back to a single-node replacement, and interrupted/expired initialization requires manual review rather than automatic replay. Existing-app menu changes must be performed separately in the developer console; this release does not offer automatic merge or repair.

Package 1.1 adds binary images and files through the existing versioned runtime. New and reauthorized apps request `im:resource` for resource uploads and `im:message:readonly` for downloading resources from received messages as tenant scopes. Receiving message events and uploading images do not prove that the app can download inbound resources. Existing installations require the missing scope to be approved and, where required by Feishu, the app version published before downloads can succeed. Updating the plugin does not silently grant Feishu permissions.

`media_download_provider_99991672` means Feishu rejected the resource download because a required API scope is missing. The official [message resource API](https://open.feishu.cn/document/server-docs/im-v1/message/get-2) accepts `im:message`, `im:message:readonly`, or `im:message.history:readonly`; this plugin requests the read-only message scope rather than the combined read/write scope. `im:resource` alone is insufficient for this endpoint. Confirm the scope on the same app used by the channel, complete Owner reauthorization or the platform approval/publish flow, then send a new image and verify an actual Task attachment. Do not infer this diagnosis from a generic failure, replay old commands automatically, or treat passing mocks as restored live downloads. See the official [generic error codes](https://open.feishu.cn/document/server-docs/api-call-guide/generic-error-code).

- Inbound events contain resource references only. The core checks the instance, Owner or group mention, conversation and Task route before issuing a durable `download_resource` command. The plugin downloads the exact message/resource pair through the official SDK and posts a bounded multipart file to that command using its lease. It cannot select an arbitrary Task for upload.
- Up to eight resources per message are accepted. PNG, JPEG, GIF and WebP images are limited to 10,000,000 bytes; files to 25 MiB. Empty, unsupported and oversized resources are reported rather than silently presented as readable attachments. Audio/video message types are outside this increment.
- Received bytes reuse the Task attachment blob store. Command-derived attachment IDs make retries idempotent, and receipt completion binds the files to the user message transactionally. A pending transfer is not silently redirected if the takeover target changes.
- SDK image Markdown referencing provider keys is replaced with a plain attachment marker, not another image URL. Web previews use the downloaded Task attachment; a marker alone does not mean the image was read. Historical messages containing only provider-key Markdown are not automatically repaired: resend after upgrading the core and plugin and granting the required media permissions.
- Agents upload result files through the Task attachment API and attach them to a turn message. The main Agent's result attachments are projected with its final reply; group conversations still receive no progress messages. Supported images within the image limit are sent as native images; larger images and other formats are sent as files within the file limit.
- Text and each attachment have separate ordered outbox entries. Uploaded provider resource keys are saved before sending; retries reuse those keys and the stable message UUID. Failed entries retain the existing dead-letter/replay behavior. File transfer is not proof that the selected model can interpret every format.

Runtime media capabilities are `channel.media.upload` and `channel.media.read`. Uploads are scoped to a download command; reads and upload-key acknowledgements are scoped to an active delivery lease. No API accepts an arbitrary local path or download URL. No credentials are included in attachment metadata.

Media failures retain safe stage codes through command retries and delivery nacks: `media_download_*` is provider download, `media_transfer_*` is inbound attachment upload to AHA, `media_read_*` is outbound attachment read, `media_upload_image_*` / `media_upload_file_*` is provider upload, and `media_record_*` / `media_send_*` is resource-key persistence / message send. Suffixes contain only a numeric provider code, HTTP status, or `failed`; raw response bodies, resource keys and transport errors are not exposed. Inbound failure notices include the sanitized diagnostic code. A generic failure is not evidence of a size or permission problem. Existing failed records cannot recover codes that were previously discarded.
