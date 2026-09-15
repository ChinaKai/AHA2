# Scoped Edge Background Adapter

Inbox319 implementation, following routed research result Inbox322.
Deployed with Owner authorization in Inbox323. This is not a universal
desktop-input implementation.

## Implemented

Desktop-bound background grants for `msedge` with a Chromium top-level window
use the standard Windows MSAA library in addition to native HWND controls.
The Agent/Owner HTTP contract is unchanged.

- MSAA roots are obtained only from the selected root and its native children.
- WindowFromAccessibleObject, native ancestry, user/session/integrity and process
  lifetime bind each accessible object to the selected window. Other top-level
  windows and popups outside that ancestry are refused.
- Read/validation phases cache repeated native host checks locally and rebuild
  the cache after capture. Every virtual node still has its state, geometry and
  native host association rechecked.
- Protected nodes expose no name/value/action and their subtree is not traversed.
  Password bounds are passed to the existing image mask and rechecked afterwards.
- Disabled native ancestors, disabled/readonly/invisible accessible controls,
  unrecognized roles and out-of-frame controls do not receive actions.
- Direct accDoDefaultAction and put_accValue are the only mutation APIs.
  There is no Focus, physical key/mouse input, clipboard, CDP or profile fallback.
- Random one-use, 30-second receipts pin target, desktop, object identity, state,
  geometry, native lifetime and watched-event generation. Restart, replay and
  stale frames require observation again.
- Current desktop and foreground/target focus guards remain active. Application
  callbacks can still cause side effects; detected focus changes quarantine input.
- No usable page document means browser_page_unavailable, with no published
  observation. Browser shell controls are not evidence that webpage control works.

## Verified Boundary

The production path was exercised through Manager shared grants with an owned
temporary-profile Edge and local HTTP fixture, without accessibility/occlusion
startup flags. After setup-only initial page activation, a separate owned anchor
holds foreground throughout navigation, Unicode input, submission and result
text reading. Password PNG masking, disabled/readonly controls, action replay
and grant revocation are checked.

Unmodified Edge on another virtual desktop did not expose the page in the
tested environment. It is explicitly refused, not silently activated. Prior
probe success with disabled occlusion is conditional evidence, not proof that
all existing other-desktop windows are supported.

Native bookmark menus, account/credential popups outside the selected HWND
ancestry, uninitialized pages and arbitrary websites are not certified by the
simple page fixture. No real mailbox was opened or logged into.

In Inbox324, the real shared Edge exposed an actionable Favorites button, but
one authorized invocation triggered focus_side_effect. Input was stopped and
not replayed; mailbox navigation/login was not completed. Page-form success
must not be presented as validation of the entire browser workflow.

## Test Harness

TestBackgroundOwnedEdgeAccessibilityProbe supports AHA_EDGE_ADAPTER=1 to exercise
the production provider/Manager rather than source-injected research probes.
The fixture uses only a temporary profile, local pages and owned windows.
Foreground initialization occurs only before the background assertions.

Optional AHA_EDGE_OTHER_DESKTOP=1 creates a temporary test desktop and records
its GUID. AHA_EDGE_EXPECT_UNAVAILABLE=1 verifies the demonstrated refusal.
AHA_EDGE_NO_OCCLUSION=1 is only a conditional research configuration, never
silently applied to the user's browser.

An early test had an incorrect defer order: Manager.Close disposed the provider
before desktop cleanup. This was fixed, and later created IDs are logged.
One unrecorded empty test desktop may remain from that failed cleanup; do not
guess its identity and close a user's desktop.
