# Background Cross-Desktop Windows (Inbox305)

Owner requested implementation, not deployment or commit.
Background mode remains limited to application windows and known safe controls,
not whole desktops or generic mouse/keyboard input.

## Contract

Main owns native/Go/HTTP/Prompt and tests. Sub-002 owns Web/tests only.
Inbox308 delivered Web results; Inbox311 delivered a read-only security review.

Status adds background_desktop_supported:boolean. A true value means the
backend can select/observe/operate a window on another virtual desktop without
switching there. It does NOT guarantee that every app exposes controls or a
rendered image. Missing/false preserves the previous fail-closed UI.

Window payload remains {kind:"window",window_id:"opaque"}; clients cannot
override desktop_id. Native background selection resolves and pins the window's
verified desktop GUID plus existing process lifetime/HWND identity. Every
observation/action revalidates that binding. A moved/closed/reused window fails
closed; no automatic desktop switch, activation, or fallback to physical input.
Only shell cloaking of a verified other-desktop window may be accepted.
Hidden/app-cloaked/inherited-cloaked helpers and foreign/elevated windows still
fail the existing checks. Stable desktop/focus checks bracket background work.

New desktop-bound background grants use bounded native child-HWND discovery inside the
validated target, not UIA child proxies. Real Windows testing showed
AutomationElement.FromHandle on a cloaked child can change focus; the guard
caught it, and that approach was removed. This path remains native-only even if
the window is currently on the active desktop: a concurrent desktop transition
must not enter UIA. Legacy unbound sessions retain their existing path.

Native Edit/RichEdit text operations and style-verified standard buttons are
supported. Owner-drawn buttons whose role cannot be determined and unknown
controls advertise no actions. Password/value redaction, ancestor disabled
states, hidden controls, geometry changes and known native mutation adapters
remain guarded. A bitmap whose password areas cannot be safely located must
not be published. Controls and mask geometry are revalidated after capture.

Native observations/actions share a dedicated background worker lane so action
receipts remain local to the worker. Actionable element IDs are random, expire
in 30 seconds, and are consumed before dispatch. Each receipt pins target,
desktop, control metadata and the watched-window event generation. Out-of-context
WinEvent notifications for watched native HWND lifecycle/state/geometry changes
invalidate receipts, including same-numeric-HWND reuse. Caret/cursor animation and
virtual child events are ignored. No code runs inside the target process. A worker
restart or missing receipt requires a fresh observation, never a reconstruction
from a client-supplied HWND. Event delivery and native operations are not an
atomic transaction; guards detect observed changes and never replay uncertain work.

## Web

Allow other_desktop windows in background mode only with
status.background_desktop_supported===true; independent of
Targets.desktop_switch_supported (background does not switch).
Preserve the foreground rule and explicit joint consent.
The background confirmation says it operates on the other desktop without
switching; it must NOT say it will switch there. Do not list desktop/new-desktop
as background targets.

Messages:
- desktop_background_desktop_changed: target desktop binding changed
- desktop_background_context_changed: current desktop changed during operation;
  result may be uncertain, no replay
- password_bounds_unavailable: image withheld because password mask location
  could not be verified

Keep shared participation, Stop, drafts, safe error states, single-flight reads,
legacy-server compatibility and all persistent toolbars.
Tests must distinguish enumerated target availability from advertised actions.

## Verification

Mock tests cover guards, errors, wiring, consent and JSON scope.
Main will also use owned Windows fixtures/temporary test desktops to check
selection, image and native control state without switching/activation during
the actual background request. Setup/cleanup may switch only test-owned state,
and must restore the original desktop. No user apps are manipulated.
Report any app/capture limitations honestly. No deployment in this round.

## Multiprocess Window Correction (Inbox314)

A selected window may embed a child HWND owned by another process. Rejecting
every such child as stale_target made the entire observation fail even though
the selected root was alive. The owner-shared Edge observation returned that
generic error; two owned fixture processes reproduced this exact defect.
The live application's precise failing branch still needs post-update validation.

Foreign-process child HWNDs now undergo separate same-user/session/integrity and
process-lifetime checks, with root ancestry rechecked. They receive no text reads
or action receipts; known native password rectangles are still masked. Failure
to verify the child withholds the observation, not a bypass or skipped mask.
Child lifetime/ancestry errors use background_control_changed and trust failures
use background_child_unverifiable, distinct from selected-root stale_target.
Prior observation authority is revoked for either new error.
