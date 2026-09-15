# Foreground Shared Control Contract

Owner Inbox 223 explicitly authorizes general foreground control, including
interference with physical keyboard/mouse, and asks for a new desktop default,
window restore/focus repair and stable dropdowns. No VM, elevation, driver or
service permission changes. This does not authorize deployment in this turn.

## Modes And Targets

Background Provider methods keep their old no-global-input behavior. Add separate
ForegroundProvider methods from internal/desktop/types.go.

POST /api/v1/tasks/{id}/desktop/session:

    {"window_id":"new-desktop","mode":"foreground","confirm_foreground":true}

`new-desktop` is the default selection and creates/switches to a Windows virtual
desktop using the supported Windows shortcut. This is a host desktop view, not a
VM or background-input isolation. Can launch applications through Start or Win+R.
Native provider returns a target `{id:"desktop:<verified desktop GUID>",kind:"desktop",
title:"...",process:"Windows"}`. Verify actual desktop identity change; do not
return success just because shortcut injection returned success. Never close an
arbitrary desktop at Stop; stop revokes control, not user applications.

Existing window ID + same mode/confirmation shares that window in foreground
mode. Missing mode retains legacy `background` behavior. New desktop requires
foreground mode. Desktop grants must be explicitly confirmed through owner UI;
agent cannot select windows, create desktops, change mode or self-authorize.
One foreground session per AHA instance at a time. A new-desktop session additionally
grants viewing/control of the created host desktop (including newly opened apps),
not only an existing process. Recheck desktop identity before capture and input:
if the user switched to another virtual desktop, fail closed instead of
silently capturing/controlling that different desktop. No claiming input
isolation. Do not automatically close the new desktop at Stop.

GET status includes `foreground_supported`; session includes `mode`.

## Observation And Action

Foreground observation includes normal PNG/width/height plus:

- `input_actions`: allowed kinds, e.g. click, double_click, scroll, text, key, drag, focus.
- `surface`: opaque target/geometry identity; checked before input.
- `elements`: may be empty; generic control must not depend on UIA enumeration.

Observation polling MUST NOT steal focus or restore minimized windows. Use
PrintWindow for existing windows, desktop capture for desktop mode; show a clear
capture_error when unavailable. Focus/restore happens on explicit action, not on
every poll. `focus` must be advertised even for minimized/temporarily unavailable
window screenshots so users can restore the target and then get fresh geometry.

POST actions keeps session_id, revision, observation_id. Foreground input uses
`element_id:"$surface"` and a listed kind:

- click / double_click: x,y in screenshot pixels, button left/right/middle.
- drag: x,y,end_x,end_y and button.
- scroll: x,y,delta_x,delta_y (pixels; each delta bounded to 2400).
- text: value, Unicode text, no clipboard.
- key: keys array using neutral names e.g. ["CTRL","L"], ["ENTER"], ["WIN","R"].
  Max 4 keys; reject unknown names. Block Ctrl+Alt+Delete and unsafe desktop-close
  chords. Always release pressed keys/buttons, including partial sends.
- focus: restore and activate only, then caller observes again.

Manager validates finite coordinates and bounds using its stored observation,
not client dimensions/surface. Pass a trusted Observation (surface,width,height)
to ActForeground. Ignore/reject input fields for background actions. Every action
is single-use and tied to latest same-actor observation; no queued stale input.
Native revalidates geometry/target before input. A changed/restored-size target
returns a refresh-required error rather than clicking in stale geometry.
Confirm target is foreground before injecting. Restoring/activating a window is
allowed only here, not legacy Observe/Act. Do not disable foreground-lock policy
or bypass UAC. Foreground activation may issue an explicit modifier tap and retry
the normal activation API, or click only a verified caption region after raising
the target without activation. No registry/foreground-lock setting is changed.
An owned same-process modal dialog can substitute for its disabled parent, with
the original grant ID retained and its owner chain verified. Unrelated/elevated
popups remain excluded. Fail with clear codes if activation still cannot be verified.
Do not inject on secure desktop, locked session, foreign user/session or elevated
target. Global stop/revoke takes precedence; short input batches only.

## Web

Single shared-control panel with foreground/background segmented mode, default
new-desktop option, explicit foreground confirmation, and current mode indicator.
Keep screen/selector/editor DOM stable across polls and chat renders. Never set
select.disabled or replace options while the user is interacting with its menu.
Fetch window list on open/explicit refresh, not repeated empty-list polling.
Separate initial loading from refresh; existing valid image remains operable
while status reads occur, and urgent stop/takeover is always available.

Foreground screenshot supports real click/double-click/drag/wheel and an explicit
Unicode text form + key shortcut buttons. Shortcut capture is opt-in via focused
image surface, not document-wide. Printable typing moves to the real text editor
and is explicitly submitted as one bounded text operation; do not drop characters
while native calls run or maintain a stale per-key replay queue. New text typed
during an in-flight submission remains a separate draft. A failed/partial send
restores the submitted draft and keeps its warning until acknowledged by another
explicit operation. Prevent default only inside the active control surface.
Offer Restore/Focus, Start and Run.
Native mouse movement can move the user's pointer, so never claim input isolation.
UI action writes serialized; key/text commands complete key up in one request.
Do not accumulate delayed input to replay after takeover. Native background
mode keeps element selection/semantic commands.

## Tests

Manager: consent, mode downgrade protection, no agent self-grant, global foreground
exclusion, create/stop race, finite/bounded coordinates, stale geometry/observation,
background fallback refusal, paused/revoked actions, active Main Turn.
Native: owned fixtures only, restore minimized and focus another window, actual
click/text/keys state changes; Windows desktop creation verified and test-created
desktop cleanup by the fixture only. Never reuse a user desktop ID for cleanup.
Web: real dropdown interaction across polling and chat updates, not just string
tests; desktop/mobile pointer/text/keyboard, background compatibility, stop races.
End-to-end checks must distinguish actual native control from mocked API tests.

## Verified Results

September 14, 2026, Windows 11 Pro:

- Main reconciled both native AHA reports and the Web report, then reran the
  Windows tests sequentially without another controller competing for input.
- Real owned Edge (temporary guest profile, normal browser sandbox retained):
  generic click to focus the address bar, Ctrl+L navigation, Unicode input and
  Enter submission to a local HTTP fixture passed. This did not use CDP or UIA
  mutation, did not operate the user's existing Edge, and did not test GitHub
  network availability.
- Real WinForms input: click/double-click/drag/wheel, Unicode, CR/LF/CRLF and Tab,
  loss of foreground, minimize/restore, stale geometry refusal and key release
  after forced cancellation passed.
- An owned modal was captured and dismissed within the parent grant; the parent
  recovered. Same-process and ownership-chain checks remain mandatory.
- New desktop creation verified a different GUID, captured a 2560x1440 image,
  and operated an owned fixture on it. Wrong GUID and actual desktop switching
  were rejected for both capture and input. Test-created desktops were cleaned.
- Web 81 tests and mock-API Playwright at 1440/390/320px passed, including the
  actual native dropdown popup across polling/chat updates and delayed input.
- Go tests, vet, desktop/httpapi race, Windows vet/build and the scoped
  desktop_panel.ts TypeScript check passed. Whole-source TypeScript checking
  still reports unrelated existing Workspace/EventTarget/nullability issues.

Protected desktop/UAC behavior is guarded but was not tested by deliberately
triggering elevation. Multi-monitor input/capture has not had an actual physical
multi-monitor test. No universal compatibility claim is made for every
application and protected surface. This verification did not deploy the code.

The Edge fixture tracks only its launched process tree and cleans it up.
Installed locations come from Windows known folders, not WSL-inherited
ProgramFiles environment variables. Generic window capture uses the physical
rectangle only when the target is foreground and unobscured; it checks again
before returning. Other cases retain non-activating PrintWindow capture, which
some GPU applications may not paint fully until explicitly focused.
