# AHA2 Feishu channel plugin

This optional process hosts the Feishu/Lark SDK. AHA2 starts one process per channel instance and passes its bootstrap capability and App Secret only through an inherited anonymous pipe. The process calls back to `/api/channel-runtime/v1`; it does not listen on a network port.

Build a package with:

```bash
./scripts/build-feishu-plugin.sh windows/amd64
```

The build script writes the executable and a manifest containing its SHA-256 to `dist/plugins/feishu/<os>-<arch>/`. Installation copies those two files into the AHA2 data directory under `plugins/channels/feishu/`. The core remains buildable and usable when this nested Go module or its binary is absent.

The plugin uses `github.com/larksuite/oapi-sdk-go/v3` v3.12.2 for Device Authorization app registration, WebSocket events, message normalization, and cards. Outbound create/reply calls use the lower-level IM API so AHA2's durable delivery key is mapped to Feishu's one-hour UUID deduplication field.

## Outbound delivery diagnostics (1.1.11)

- Feishu can answer HTTP 200 with a non-zero business code. Outbound sends now classify both layers into stable codes such as `feishu_target_unavailable`, `feishu_bot_disabled`, `feishu_permission_denied`, `feishu_rate_limited`, `feishu_reply_target_unavailable`, `feishu_provider_unavailable`, and `feishu_*_rejected_<code>`.
- A reply whose parent message was withdrawn or deleted (`230011`) now falls back to a fresh message in the same chat. Retrying the same reply could never succeed and previously consumed all attempts before dead-lettering.
- NACK requests include the provider request ID so the core records a traceable attempt without persisting raw responses.
- Permanent target/permission errors dead-letter immediately; rate limits and unknown provider failures retry with full-jitter backoff up to 12 attempts; ambiguous transport failures retry only inside Feishu's one-hour deduplication window.
- `delivered` remains provider acceptance only. The core records `outcome_certainty=provider_accepted`; it is not proof that the recipient's client displayed the message.

## Reliability (1.1.10)

- The SDK read deadline detects a silent connection loss (device sleep, network change, NAT timeout) and reconnects instead of leaving `ReadMessage` blocked forever. A WebSocket disconnect still marks the instance `degraded / feishu_reconnecting`; `OnReady` and `OnReconnected` both report `ready`.
- The plugin sends a health heartbeat every 30 seconds with `last_inbound_at`. The supervisor marks a plugin without a heartbeat for 90 seconds as `degraded / channel_heartbeat_timeout` and restarts it.
- Channel runtime capabilities slide forward on authenticated use, so a continuously polling plugin does not lose its capability at the original 24-hour boundary.
- Plugin stderr is retained in the control-plane log instead of being discarded.

## Cloud document read (1.1.5)

Messages containing up to three Feishu wiki or docx links now include the
resolved document text in the same untrusted inbound message. Wiki links are
resolved with `GET /open-apis/wiki/v2/spaces/get_node`; docx content is read
with `GET /open-apis/docx/v1/documents/:document_id/raw_content`. The plugin
only accepts links whose host is under `feishu.cn`, `larksuite.com`, or
`larkoffice.com`, applies a five-second total timeout, and caps appended source
text at 24,000 runes.

The optional `cloud_docs_read` permission requests `wiki:node:read` and
`docx:document:readonly`. API scope alone is insufficient: the AHA2 app must
also be granted access to the specific document, or to the containing wiki
space. Existing applications must explicitly select the new permission through
reauthorization. AHA submits the application release after Feishu accepts the
incremental scopes; administrator approval and the final release state remain
authoritative on Feishu.

## Reply and quote context (1.1.4)

The receive event exposes `parent_id` and `root_id` for replies, but not the
quoted message body. When `parent_id` is present, the plugin calls
`GET /open-apis/im/v1/messages/:message_id` with sender names enabled and sends
the normalized parent as a separate `reply_context` field. The core injects it
into the Inbox as untrusted data rather than concatenating it into the current
sender's text. A failed parent lookup does not drop the incoming message; it
produces an `unavailable` context with a stable, sanitized error code.

## Group topics and full message reception (1.1.12)

After upgrading, open the Feishu channel's permission page, select **群消息全量接收**, and scan the QR code again to authorize the existing app. Feishu may require an administrator to approve the sensitive application scopes. The permission is off by default. For full reception, bind the group to a Task and add its `oc_…` chat ID to the channel's **飞书群消息白名单** in Settings. An explicit @mention still works outside the allowlist. In a new group, the first @mention starts the group host Task and gets a reply. If the group's bound Task finished, an @mention can continue it in a new Task.

Feishu documents `im:message.group_msg` for user messages and
`im:message.group_msg.include_bot:read` for user and other-bot messages.
`im:message.group_at_msg.include_bot:readonly` covers messages from other bots and users that mention this bot. Full reception applies to the app's groups at Feishu; the AHA allowlist controls local processing. Confirm the new scopes and actual non-mention delivery in a bound test group after authorizing. See the [Feishu receive-message event](https://open.larkenterprise.com/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/message/events/receive).

Each event retains its message-level thread ID, root ID and sender. Interactive
cards expose the visible text and button labels included in the event. Unmentioned main-chat messages are stored as background for seven days, with at most 200 per group and 4 KiB injected into a matching Turn. Removing a group from the allowlist or disabling full reception stops background injection immediately. Existing background is deleted by the hourly expiry check after seven days. In a bound group, AHA groups wake events into six-second windows. It allows up to three consecutive bot-only Turns and 12 Turns per rolling hour per topic. Humans who @mention the bot in the main chat are exempt and do not use the main-chat bot hourly budget. Rate-limit outcomes appear on the receipt and in the conversation's latest inbound status if it is still the newest event. Retries of a topic Turn also count toward its limits. When a retry exceeds a limit, the caller receives and logs the error without sending a separate notice. The channel page shows when a non-mention bot message in a topic was last observed. Verify this in a bound, allowlisted test group after reauthorization. A plugin connected to a core that predates topic support sends the original v1 @-only group envelope.

## Images and files

### Permission reauthorization and release (1.1.7)

Existing-app QR authorization uses the SDK's incremental scope Addons and targets the same App ID. It does not send creation presets, names, events, callbacks, bot abilities, or menus. The server still enforces the same unique Owner, and the scanner explicitly confirms the permission change on Feishu's official page.

After Feishu accepts an existing application's incremental authorization, AHA
submits one ordinary application release through the official publish API. It
does not patch menus, events, callbacks, or bot abilities during this step. A
release failure fails onboarding with a stable `registration_publish_*` error
code instead of reporting authorization as complete. Administrator approval
and the final release state remain authoritative in Feishu.

The release flow also runs when no new permission is selected. Because the
registration SDK requires a non-empty additive payload for reauthorization,
AHA uses the existing `im:message:readonly` scope as a no-op anchor. The scanner
still confirms the flow and the successful scan triggers the application
release.

Process startup and credential rotation no longer enqueue menu configuration. Queued legacy `configure_menu` commands complete as skipped without provider requests, including retries. New applications alone receive a transactional, onboarding-scoped `initialize_menu` command. The creation dialog explicitly discloses that initialization submits a release. Initialization is attempted only once within five minutes, against the newly created App ID; reauthorization cancels outstanding initialization. Rejected menus do not fall back to a single-node replacement, and interrupted/expired initialization requires manual review rather than automatic replay. Existing-app menu changes must be performed separately in the developer console; this release does not offer automatic merge or repair.

Package 1.1 adds binary images and files through the existing versioned runtime. New and reauthorized apps request `im:resource` for resource uploads and `im:message:readonly` for downloading resources from received messages as tenant scopes. Receiving message events and uploading images do not prove that the app can download inbound resources. Existing installations require the missing scope to be approved and, where required by Feishu, the app version published before downloads can succeed. Updating the plugin does not silently grant Feishu permissions.

`media_download_provider_99991672` means Feishu rejected the resource download because a required API scope is missing. The official [message resource API](https://open.feishu.cn/document/server-docs/im-v1/message/get-2) accepts `im:message`, `im:message:readonly`, or `im:message.history:readonly`; this plugin requests the read-only message scope rather than the combined read/write scope. `im:resource` alone is insufficient for this endpoint. Confirm the scope on the same app used by the channel, complete Owner reauthorization or the platform approval/publish flow, then send a new image and verify an actual Task attachment. Do not infer this diagnosis from a generic failure, replay old commands automatically, or treat passing mocks as restored live downloads. See the official [generic error codes](https://open.feishu.cn/document/server-docs/api-call-guide/generic-error-code).

- Inbound events contain resource references only. The core checks the instance, Owner or group mention, conversation and Task route before issuing a durable `download_resource` command. The plugin downloads the exact message/resource pair through the official SDK and posts a bounded multipart file to that command using its lease. It cannot select an arbitrary Task for upload.
- Group conversations are scoped by chat rather than sender. The sender and mentioned users are normalized separately for every message, while raw provider user/chat IDs remain inside the channel runtime boundary. A routed Task replies by receipt to a group @mention or a message in an existing topic. The reply stays in the source topic and falls back to the topic root if the original message was withdrawn. If the root ID is missing or that root was withdrawn too, delivery records a failure and never posts in the main chat. A reply automatically mentions its sender. Explicit mentions use the verified Task contacts.
- Group member sync uses `member_id_type=open_id`, fetches every page, and returns the complete roster to core in one response. A repeated `page_token` ends the sync with an error. The sync relies on Feishu's group-size limit. Returned entries are users because Feishu's member-list API excludes bots. Other bots become selectable when observed sending messages or being mentioned.
- Up to eight resources per message are accepted. PNG, JPEG, GIF and WebP images are limited to 10,000,000 bytes; files to 25 MiB. Empty, unsupported and oversized resources are reported rather than silently presented as readable attachments. Audio/video message types are outside this increment.
- Received bytes reuse the Task attachment blob store. Command-derived attachment IDs make retries idempotent, and receipt completion binds the files to the user message transactionally. A pending transfer is not silently redirected if the takeover target changes.
- SDK image Markdown referencing provider keys is replaced with a plain attachment marker, not another image URL. Web previews use the downloaded Task attachment; a marker alone does not mean the image was read. Historical messages containing only provider-key Markdown are not automatically repaired: resend after upgrading the core and plugin and granting the required media permissions.
- Agents upload result files through the Task attachment API and attach them to a turn message. The main Agent's result attachments are projected with its final reply; group conversations still receive no progress messages. Supported images within the image limit are sent as native images; larger images and other formats are sent as files within the file limit.
- Text and each attachment have separate ordered outbox entries. Uploaded provider resource keys are saved before sending; retries reuse those keys and the stable message UUID. Failed entries retain the existing dead-letter/replay behavior. File transfer is not proof that the selected model can interpret every format.

Runtime media capabilities are `channel.media.upload` and `channel.media.read`. Uploads are scoped to a download command; reads and upload-key acknowledgements are scoped to an active delivery lease. No API accepts an arbitrary local path or download URL. No credentials are included in attachment metadata.

Media failures retain safe stage codes through command retries and delivery nacks: `media_download_*` is provider download, `media_transfer_*` is inbound attachment upload to AHA, `media_read_*` is outbound attachment read, `media_upload_image_*` / `media_upload_file_*` is provider upload, and `media_record_*` / `media_send_*` is resource-key persistence / message send. Suffixes contain only a numeric provider code, HTTP status, or `failed`; raw response bodies, resource keys and transport errors are not exposed. Inbound failure notices include the sanitized diagnostic code. A generic failure is not evidence of a size or permission problem. Existing failed records cannot recover codes that were previously discarded.
### Selectable permissions and runtime menus (1.1.2)

Existing applications keep additive-only Feishu permission authorization. AHA
stores the selected feature permissions after a successful authorization and
new applications initialize the default AHA bot menu. Reauthorization itself
still does not rewrite an existing Feishu menu tree; the explicit reset command
is introduced in 1.1.3.

Permission-catalog additions are opt-in. If an existing instance predates the
stored `channel_permission_ids` list, AHA assumes only the original catalog,
not every feature added later. The update dialog lists the exact permission
names in its request summary and disables confirmation when there is nothing
new to request. For an existing application, only scopes for newly selected
features are sent to Feishu; already enabled permissions remain in the stored
union but are not resubmitted on every unrelated permission update.

### Explicit menu reset (1.1.3)

`reset_menu` is an Owner-triggered initialization for an existing application.
It rewrites the Feishu bot menu to the AHA default menu and submits the normal
application publish flow. It is not an AHA runtime response switch. AHA always
handles all known menu actions after the application is ready.
