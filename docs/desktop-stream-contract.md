# Low Latency Shared Desktop

Owner Inbox 235 asks to fix stale-frame rejection and improve WAN operation
towards real time with adaptive resolution. No automatic deployment, persistent
preview, external infrastructure, VM, permission or firewall changes.

## Native (sub-001)

Keep existing Provider/ForegroundProvider behavior and protection. Add
`PreviewProvider.ObservePreview(ctx, window, FrameOptions)` using types.go.
FrameOptions max_width/max_height/quality are server validated. Produce JPEG,
resize BEFORE image encoding, preserve aspect ratio, never upscale. Default
1280x720 quality 65; bounds width 320..1920, height 180..1080, quality 35..85.
Observation `width,height,surface` remain ORIGINAL input coordinates/identity.
`preview_width,preview_height,mime` describe the actual JPEG bitmap. `image`
remains base64 on the local helper pipe/legacy JSON path; Web transport strips it
and sends binary JPEG. Legacy direct ObserveForeground may retain full PNG for
compatibility/native fixtures.

Replace per-call PowerShell/Add-Type with resident helper(s), compiling fixed
source once and exchanging bounded length-delimited or line JSON requests with
request IDs. Separate capture and action workers so image work cannot starve
Stop/input. Preserve Job containment, sanitized environment, bounded outputs,
timeouts, no speculative action retry, canceled-batch key release and foreground/
desktop/owner-chain safeguards. Parent must kill/reap a failed worker and discard
its pending reply, not replay an uncertain action on a restarted worker.
Provide `Close() error` for cleanup; idle reaping (e.g. 30s) and parent death must
also stop workers. No setup token or AHA capability passed to helpers.

Native tests: repeated warm capture latency and JPEG size at multiple scales,
aspect/logical-coordinate invariance, resident reuse, restart/cancel and input
not queued behind capture. Only owned Windows fixtures; no user window controls.
Return actual cold/warm measurements, not a real-time claim from compilation.

## Manager / HTTP (Main)

Bounded recent snapshots replace single latest-per-actor storage. Frames are not
invalidated by a newer observation; keep at most 64 foreground metadata snapshots
per view, 4 background, TTL 30s, max 8 owner views per session. Never retain image
bytes or value/name text just for action validation. Legacy request without
action_id consumes its referenced snapshot once; new requests with action_id
may reuse a still-valid displayed frame but each action ID executes at most once.
Bound replay records and fail closed when full; don't evict a live action ID
and permit it to replay. Control transfer/stop clears frames and revokes operations.

Web requests carry random per-mount `view_id` (1..64 ASCII alnum/_/-). Owner actor
namespace derives from authenticated user request, not arbitrary client actor.
Agent requests reject view_id and retain turn-bound actor. Each view sees only
its frame IDs. Inputs always recheck task/session/revision/controller, native
surface/geometry and desktop identity. Action ID is optional 1..64 ASCII token.

Foreground capture and input use independent cancellation/serialization lanes.
Input must not wait for a slow frame capture or frame download. Discard captures
crossing an action epoch rather than publishing a pre-action result afterwards.
Background mode remains conservative. No automatic retry of uncertain actions.

Capture scheduling covers snapshots, live previews, and window/target enumeration.
Use one bounded native capture lane: interactive reads precede the next waiting
stream frame, with at most three consecutive priority dispatches when a preview
is waiting. Preserve FIFO within each class. Queue at most 64 requests globally
and 16 observations per session; include queue time in the operation deadline
and grant expiry. Ordinary preview contention waits inside the same request,
not an Agent-side busy/retry loop. Legacy Agent reads still return full captures,
never a cached/downscaled viewer frame.

Queued reads are canceled on stop, transfer, switch, expiry and manager close.
Recheck session identity/revision, actor/controller, stream lease and cancellation
after admission, before native work. Stream disconnection or transient task-read
failure closes that request without revoking a valid grant; confirmed task removal
or terminal state still revokes sharing.

## WebSocket Protocol (Main / sub-002)

GET `/api/v1/tasks/{id}/desktop/stream?session_id=...` uses existing session auth.
No tokens in URL. Always enforce same-origin for this sensitive socket.
First TEXT message (within 5 seconds) must be:

    {"type":"start","csrf_token":"...","view_id":"...",
     "max_width":1280,"max_height":720,"quality":65,"interval_ms":100}

sub-002 may add `api.csrfToken()` read-only getter for this handshake. Never log
the handshake/token. Server validates CSRF and active, non-terminal shared
foreground session BEFORE any capture, then sends:

    {"type":"ready","protocol":1,"status":{...}}

Frames are single BINARY WebSocket messages:

    4-byte unsigned big-endian JSON length
    UTF-8 JSON metadata
    raw JPEG/PNG bytes (no base64)

Metadata:

    {"type":"frame","observation":{...,"image":"","mime":"image/jpeg",
      "preview_width":1280,"preview_height":720,"width":2560,"height":1440},
     "capture_ms":40,"interval_ms":100}

JSON limit 64KB, image limit 8MB; reject malformed packets/unsupported mime.
Client decodes and displays, then sends TEXT ack:

    {"type":"ack","frame_id":"...",
     "max_width":1280,"max_height":720,"quality":65,"interval_ms":100}

Server allows at most TWO unacknowledged frames (bounded backpressure). It does
not capture/queue a frame per timer when the client is slow. Drop stale captures,
never build an unbounded backlog. Timeout stalled readers. TEXT messages may also
be `{"type":"status","status":...}` or `{"type":"error","error":"desktop_..."}`.
No input messages on the video socket: existing HTTP actions/control/stop remain
separate so a slow frame write cannot block control handling.

Recheck owner authentication lifetime and task/session/controller revision while
streaming; stale socket must not outlive logout, stop or expiry. Disconnect drops
snapshots produced by that connection but preserves newer HTTP fallback frames
for the same view. It does not revoke another view's grant or close apps.

## Web UX (sub-002)

Use streaming for foreground when `status.stream_supported` is true; keep
background/compatibility polling, with lower-cost query options for foreground
fallback. The default mode is auto: adapt scale/quality/interval using measured
frame cycle, payload size and decode time with hysteresis. Provide restrained
auto/quality/low-bandwidth selector and resolution/FPS/latency indicators.
Logical input mapping MUST use observation.width/height, not JPEG naturalWidth.
HTTP actions include view_id and crypto-random action_id; preserve displayed
frame while a pointer gesture is active, dropping/acking newer frames safely.
No delayed input replay after takeover/disconnect; HTTP input stays serialized.
Printable text may continue through the reliable existing editor.

Do not reconnect into unauthorized/expired session or hide transfer/partial
action errors. Close socket/revoke Blob URLs/clear timers on unmount, task change,
hidden page, logout and stop; reconnect boundedly on visible transient failures.
If WebSocket is blocked, fall back honestly to adaptive HTTP observations, not
the old 3.6MB/2.5-second path. Keep native select popup DOM stable across frames.

## Verification

Manager: delayed delivery of older valid frame, simultaneous views, consume/replay,
revocation, TTL/count caps, capture-vs-input cancellation/priority.
Continuous multi-view preview must not starve one Agent screenshot request; bounded
priority must also allow previews to progress. Verify queued cancellation, stream
lease replacement, enumeration serialization and input independence.
HTTP/WS: CSRF, origin, task/owner isolation, logout/expiry/stop, malformed/oversized
messages, two-frame credit bound and slow reader; input unaffected by pending
capture/write. Agent legacy compatibility and a single successful Agent HTTP
read while a live socket is capturing. Canceled/transient task lookups preserve
sharing; confirmed missing or terminal tasks do not.
Web: delayed/throttled mock transport, adaptive downgrade/recovery, input coordinate
mapping at different resolutions, dropdown/gestures/drafts and reconnect cleanup.
Native: actual owned fixture warm performance + compressed sizes, input regression.
Report simulated WAN separately from real public-network measurement.
