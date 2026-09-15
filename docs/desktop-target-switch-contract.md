# Shared Targets and Title Bar (Inbox 251)

Owner approved title-bar consolidation, direct window changes, single physical
monitor selection and reusable virtual desktops; deployment after acceptance.
Clash Verge no-window diagnosis is explicitly REMOVED from scope.
Do not modify proxy settings or manipulate user applications for testing.

## Ownership

- sub-001: native*.go/cs/ps1 and foreground native helpers/tests, new native target
  files. Do not change manager.go, frames.go, types.go or HTTP/Web.
- sub-002: web/src/desktop_panel.ts, task_tools.ts, related scoped main.ts changes,
  styles.css, existing icons and Web tests. Do not change native/manager/HTTP.
- Main: types.go (contract added), manager, HTTP, integration and deployment.

## Shared Go/JSON types (already in types.go)

Window gains desktop_id, monitor_id metadata. Window.id remains OPAQUE to Web
and must pin all capture/input scope (virtual desktop GUID, monitor identity).
Window.kind remains window or desktop for compatibility.
Structured window choices also carry optional desktop_name and other_desktop
(missing means false). Never allow the client to override a window's desktop
location in the selection payload.
For TargetProvider-selected desktop/new-desktop, return nonempty desktop_id and
monitor_id even when the request selected the default primary monitor.

Monitor: id, name, x, y, width, height, primary.
VirtualDesktop: id, name, current.
Targets: windows[], monitors[], desktops[], desktop_switch_supported,
desktop_reason (optional safe code).
TargetSelection: kind = window|desktop|new-desktop; window_id, desktop_id,
monitor_id optional as appropriate.
For window use ONLY window_id (no desktop_id/monitor_id); desktop requires
desktop_id and optional monitor_id; new-desktop accepts only optional monitor_id.
TargetProvider: Targets(ctx) (Targets,error);
SelectTarget(ctx, TargetSelection) (Window,error).
Session gains switching bool. Status gains targets_supported bool.

## Owner HTTP contract (Main)

- GET /api/v1/tasks/{id}/desktop/targets returns {ok:true,targets:{...}}.
  Owner only, no Agent enumeration or selection APIs.
- POST .../desktop/session keeps old window_id/mode/confirm_foreground fields,
  also accepts target: TargetSelection. New Web should use target.
  Default foreground new-desktop preserved; background permits window only.
- POST .../desktop/switch accepts
  {session_id,revision,target:{...},mode:"foreground|background",
   confirm_foreground:true|false}.
  Owner only; every foreground selection needs explicit confirmation of keyboard/
  mouse/desktop effects. Modal confirm OK. Do not auto-inherit Agent control.
- Switch revokes old operations/frames/grant immediately after request validation,
  increments revision and reports switching=true. Wait for old input/capture to
  exit before selecting any new target. Stop can cancel pending selection.
  New session has a NEW random session_id, revision=1, claimed=false.
  With explicit confirm_shared:true it is jointly authorized (controller=shared);
  without it legacy requests remain controller=owner. Old request/claim/frame/
  action IDs cannot reach the new target.
  On selection failure, old grant is NOT restored, status has no session.
- Actions/capture/control/claims fail closed with desktop_switching during a
  transition. UI must re-read status on failure, never replay uncertain input.
- Malformed selection or missing confirmation must not revoke an existing valid
  session. IDs are bounded (window/monitor 128 bytes, desktop 64 bytes). A target
  that disappears or fails native selection leaves sharing stopped, as above.

## Native (sub-001)

Implement TargetProvider; preserve resident lanes, cancellation cleanup, Job and
all existing desktop/integrity/session/window-lifetime protections.
Targets is metadata-only enumeration. Existing virtual desktops and current ID,
physical monitors with actual pixel origins and sizes, and windows.
Legacy Windows enumeration remains current-desktop-only. Structured Targets can
include other desktops' windows after public COM location/current-state checks
and membership in the verified desktop list. Only DWM_CLOAKED_SHELL may be
relaxed for that verified other-desktop window; app/inherited cloaking still
rejects it. Revalidate location across metadata inspection.
Window choices share InspectPickerWindow eligibility (Inbox275): retain
visibility and allowed-cloaking checks plus
same-user/session/integrity checks, exclude the shell desktop surface, tool and
non-activating helper windows unless explicitly marked APPWINDOW, and require
a positive client area. Minimized windows use their normal placement bounds
instead, so they remain selectable for restoration. Keep legitimate untitled,
borderless and owned dialog windows; do not classify by process name or title.
This is picker-only filtering, not a new restriction on an already-shared
window's identity validation. It must not capture or activate windows to decide.
Only explicit Owner foreground selection may switch to an other-desktop window.
Validate its original process/HWND identity and desktop before switching and
again after the verified transition. Window moves, closure, identity reuse or
inconsistent desktop state must not create a grant.
Providers advertising background_desktop_supported also resolve background
window selection through a metadata-only path and pin its verified desktop GUID.
Every background read/action revalidates that binding and preserves the current
desktop/focus; it never calls the foreground selector. See
background-cross-desktop-305.md for native-adapter and capture limitations.
Use proven OS/libraries rather than speculative version-dependent COM vtables.
If enumeration relies on undocumented registry state, validate against the
existing COM current-GUID probe and fail closed on inconsistent state.
Owner SelectTarget may switch desktops through bounded verified transitions.
No arbitrary input on user apps, no elevation/driver/install/firewall changes.

Selecting desktop/new-desktop binds one monitor (default primary), not the
stitched virtual-screen rectangle. Capture only that monitor. Frame dimensions
are monitor-local logical pixels; native input adds its physical origin and uses
existing virtual-screen normalization. Target identity/Surface must bind monitor
ID AND geometry; disconnect/reconfigure fails closed, never silently picks a
different display. Keyboard/text must not land in a foreground window outside
the selected monitor. Preserve same-virtual-desktop checks on every operation.

NewDesktop legacy API should also choose a single primary monitor. Keep legacy
direct test callers compatible where safe; update GUID parsing in owned fixtures
to use Window.DesktopID if new opaque IDs carry monitor scope.

Tests: source/geometry tests for negative origins and differing dimensions,
real current-monitor capture, create OWN desktops then switch/reuse and clean
ONLY owned desktops. Main runs no physical tests concurrently. Do not switch
existing user desktops/apps for tests. Report hardware limitations honestly;
single-display hardware cannot prove real multi-display switching.

## Web (Inbox 272 Layout Revision)

Keep one actual "shared control" task title bar. Shared controls stay left and
page layout/close stay separately right; title-bar icons expose hover titles.
Quality/fit/zoom/fullscreen occupy one persistent 40px row beneath the header.
Foreground shortcuts and the text editor/send command occupy one persistent
44px row below the image viewport. At narrow panel widths, keep Enter/Esc and
the editor visible, and put other shortcuts in an accessible More menu.
No nested scrolling control cards or automatic multi-row expansion. Fit the
image into remaining width AND height, preserving aspect ratio and all edges.
Owner accepts this modest height cost instead of hiding frequent controls.

Keep Stop and restore focus directly accessible. Inbox288 replaces the
Owner/Agent handoff buttons with explicit joint consent on sharing; after that
both may participate, with single-flight actions and fresh per-view observations.
Target selection and elements/status remain popovers, positioned below the
quality row so they cannot obstruct its controls. Quality changes and hover
must not dispatch native input or steal the local editor focus; explicit remote
input/focus actions retain all existing authorization and freshness checks.
Target picker may contain desktop and window groups plus
monitor selection; new desktop creation separate from existing desktop selection.
Allow direct switch while shared through new /switch endpoint, not client-side
Stop+Open chaining. Invalidate current input/old stream before switching; confirm
foreground effects and joint access; never inherit a prior claim/observation
into the new target. Never auto-retry input.
Keep error/revocation notices visible (compact alert overlay is acceptable).
Empty/unshared view can open target picker; hide the keyboard row when no
foreground session exists. Keep local viewing controls available after Stop.
Retain fit/fullscreen/zoom, coordinates, menu stability, drafts, production CSP,
bounded WS/HTTP fallback and cleanup across remount and task changes.
Integrate real existing outer task header (not a duplicate "shared control" row).
Do not use portal moves that strand close/layout handlers or orphan controls
on fullscreen exit/unmount. Verify real renderTaskToolPanel + main-like lifecycle.

Run Node/scoped TS/build and Playwright 1440/390/320 plus short landscape.
Verify single-line control rows at desktop/mobile/landscape and 300..420px
desktop split widths, complete fitted image, single header, read-only roundtrip,
popover outside click/Escape/focus,
stop available with popover open, old input not replayed, new session selection,
monitor/desktop groups, full-screen cleanup and preserved menu interactions.
Mock host/network does not equal physical/native/WAN acceptance.
