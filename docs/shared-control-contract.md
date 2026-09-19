# Shared Host Window Control

Implementation contract. Windows host only initially; no VM, no clipboard writes
or elevation. Background mode has no global input or focus-setting API.
Owner-confirmed foreground mode intentionally uses the physical keyboard/mouse
and restores/focuses the shared target. See `foreground-control-contract.md`.
Other hosts return an
explicit unsupported status. Use the existing `browser` task tool ID for layout
compatibility, but display its label as shared control.

## Owner API

All endpoints use existing session authentication and CSRF policy.
Prefix: `/api/v1/tasks/{id}/desktop`.

- `GET /`: actually GET the prefix without trailing slash; `{ok,status}`.
- `GET /windows`: `{ok,windows:[{id,title,process}]}`.
- `POST /session` with `{target,mode,confirm_foreground?,confirm_shared:true}`:
  `{ok,status}`. Explicitly grants joint Owner/Main Agent access, with
  `controller:"shared"`. Foreground also requires `confirm_foreground:true`.
  See `shared-cooperative-control-288.md`. Requests without shared consent
  retain legacy Owner-only behavior; old grants are never silently broadened.
- `POST /control` with `{session_id,revision,controller:"owner"|"agent"}`:
  Legacy exclusive sessions only. Shared sessions reject this without mutation.
- `POST /stop` with `{session_id}`: `{ok,status}`. Revokes the entire session.
- `GET /observation?session_id=...`: `{ok,observation}`.
- `POST /actions` with `{session_id,revision,observation_id,kind,element_id:"$surface",...}`:
  `{ok,status}`. Both participants can use a shared grant. Input is bounded and
  always addresses `$surface`, tied to the latest observation. No direct command
  execution, arbitrary window IDs or clipboard operations are exposed.

Control is foreground-only. The background adapter was removed because its
element actions could not carry ordinary pointer input, and a control mode that
cannot navigate or click where the user points was not usable. Consequently the
accepted kinds are `click`, `double_click`, `drag`, `scroll`, `text`, `key` and
`focus`; the element kinds (`invoke`, `set_value`, `toggle`, `select`, `expand`,
`collapse`) are refused.

Status and observation types are in `internal/desktop/types.go`.
Observation contains PNG base64 image (may be empty with capture_error) and
window-relative element rectangles. The element list is a read-only inspection
aid: it is not an input surface. Never store screenshot or element contents in
localStorage or logs. Treat all captions as untrusted text.

### Current Windows Adapters

Control goes through the foreground contract: the Owner grants a target, AHA
brings it to the foreground, and input is dispatched as real pointer and
keyboard events against the observed surface. The element list is read-only
inspection and carries no actions.

No endpoint exposes native message IDs or HWND parameters to a caller.
Class, process lifetime, ancestry, control ID and current element identity
are validated before each input. Each native command is bounded.

The opt-in native fixture tests actual image capture, password masking,
text/checkbox/radio/button state changes, and foreground window/input focus
before and after each operation. Application callbacks may independently open
dialogs or change focus; a detected side effect pauses and quarantines the
target. This is not a guarantee about arbitrary third-party application behavior
or a replacement for application-specific compatibility testing.

## Agent API

Prefix: `/api/v1/agent/desktop`. Existing Task/Agent/Turn bearer capability
and active Main turn required. No capability to enumerate or select arbitrary
host windows or grant itself access.

- `GET` returns `{ok,status}`.
- `GET /observation?session_id=...` returns `{ok,observation}` while granted.
  Shared grants automatically register current-Turn use and publish a notice
  without interrupting Owner viewing or changing controller/revision.
- `POST /control` with `{session_id,revision,controller:"agent"}` claims a
  session already granted by the owner, publishing the target software notice
  before marking the session claimed.
- `POST /actions` uses the owner action schema; shared grants automatically
  register current-Turn use, legacy Agent grants require the explicit claim.

## Lifetimes

One selected target per task; a window cannot be shared by multiple tasks.
Only one foreground grant may exist on a host. Creating a new desktop also
reserves this slot; cancellation does not permit overlapping input workers.
Session grants are memory-only and expire after 30 minutes, requiring explicit
owner renewal. Restart revokes every grant. Stop works even during native calls.
Legacy control changes increment revision. Shared participation does not.
Observations expire after 30 seconds; actors/views keep separate frame metadata.
Owner preview polling does not invalidate Agent plans. A shared participant's
actual action invalidates all other participants' old frames before native work.
The other participant must observe again after assistance. Stop, target changes
and native failure revoke prior frames/operations. Shared native errors do not
transfer the grant to an exclusive role or allow replay of the same action ID.
Stale requests and concurrent native actions fail rather than queue.
Native operations have bounded execution time; cancellation cannot undo an
operation already accepted by the target application.

## UI

Use `renderDesktopPanel(detail)`, `bindDesktopPanel(detail, notify)`,
`stopDesktopPanel()`, and `refreshDesktopPanel(detail, notify)`.
Keep module-scoped per-task state; avoid rerendering the panel on every chat
update. Poll only while mounted and visible, with at most one observation in
flight. Owner can inspect element list, select a control by screenshot hit
testing, invoke supported operations and set values through an explicit form.
Background mode has no free-form keyboard passthrough. Foreground mode provides
direct image clicks/drags/wheel, a Unicode text form and bounded key chords.
Provide window chooser, process/title, refresh, joint share, stop, error/status,
image, elements and supported action controls. There is no Owner/Agent handoff
button. Joint sharing includes explicit consent; old sessions remain legacy
until the Owner re-shares. Quality and keyboard rows remain persistent.
Use existing icon library and CSS conventions; desktop/mobile layouts tested.
Dropdown options/disabled state must not mutate during active selection;
periodic reads must not tear down the control surface or invalidate UI focus.

## Verification

Use the repository Go toolchain. Keep temporary files inside the workspace.

```sh
node scripts/build-web.mjs
node --test web/tests/*.test.mjs
.tools/go/bin/go test ./...
.tools/go/bin/go vet ./...
.tools/go/bin/go test -race ./internal/desktop ./internal/httpapi
node web/tests/desktop_panel.browser.mjs
GOOS=windows GOARCH=amd64 .tools/go/bin/go test -c \
  -o .data/shared-control-test/desktop.test.exe ./internal/desktop
```

The browser test uses mock API responses, exercises 1440/390/320px viewports
and keeps screenshots under `.tools/web-validation/screenshots`. It requires
the workspace-local Playwright/Chromium packages used by that script.

`AHA_DESKTOP_FIXTURE_TEST=1` opts into `TestNativeOwnWindowFixture` on Windows.
It opens only its own non-activating WinForms window, with standard Win32
Edit/Button children, and checks actual state changes for every enabled action.
It also checks PNG capture, password masking, password action refusal, and
foreground window/input focus stability. It does not enumerate the user's
existing windows. `AHA_NATIVE_HELPER_TEST=1` enables malformed-target smoke.

Windows executables can be run directly through WSL interoperability. Keep
their TEMP/TMP in the workspace and map them using WSLENV `/p`. The Windows
application's SQLite database may not work on the WSL UNC share (observed
SQLITE_BUSY during schema creation); do not work around this by writing to an
unauthorized host directory. Deployment to the real Windows installation is a
separate, explicitly authorized operation.
