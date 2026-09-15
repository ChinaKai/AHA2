# Desktop Capture and Cross-Desktop Window Choices (Inbox280)

Owner requested a fix, not a new deployment. Preserve Inbox275 picker changes.
No user desktop switches or input for tests; never bypass an Agent grant.

## Main

Native/Go/HTTP ownership. Remove the desktop capture dependency on the
foreground window's visual eligibility, while keeping user/session/integrity,
input-desktop and monitor/GUID checks. Input remains separately validated.
An unready foreground can disable input without discarding an authorized frame.
Return precise safe rejection codes; do not expose process details.
Specifically, capture still verifies the foreground process identity/trust.
A trusted but hidden/cloaked foreground yields a read-only image. Missing/dead
foreground yields a retryable foreground_unavailable error; protected or foreign
processes still reject capture. Changed foreground across capture can retain
the same desktop/monitor frame with zero advertised input actions.

Structured Targets may include other virtual desktops' windows only when
validated by public IVirtualDesktopManager and a verified desktop list.
Only shell cloaking attributable to a different desktop may be relaxed for
listing. App-cloaked/hidden helpers still fail closed.
Only explicit Owner foreground selection may switch to that window's desktop.
The target JSON remains {kind:"window",window_id:"..."} with no desktop override.
After selection revalidate original process/HWND identity and desktop location.
Background window enumeration remains restricted to the current desktop.

## Web (Sub-002)

Own web/src/desktop_panel.ts and focused Web tests only. Do not change Go/native.
Window metadata adds optional desktop_name:string and other_desktop:boolean;
desktop_id already exists. Missing other_desktop means false for compatibility.

Show window desktop names in choices. Other-desktop windows are not selectable
in background mode, or when Targets.desktop_switch_supported is false.
The foreground confirmation must clearly mention the other desktop and switch.
Never switch merely on list selection, refresh or polling.
Keep payload selection {kind:"window",window_id}, with current explicit
foreground confirmation, stale-input cancellation and no inherited Agent grant.

Add safe messages for:
- desktop_target_state_unavailable
- desktop_target_process_unavailable
- desktop_target_token_unavailable
- desktop_target_foreign_session
- desktop_target_foreign_user
- desktop_foreground_unavailable
- desktop_elevated_target (existing)

An observation can return a valid image with existing control_error set to
desktop_foreground_unavailable/desktop_foreground_changed and input_actions=[];
retain the image but never enable input or focus for that frame.
Keep persistent rows, responsive layout, draft/readonly/remount/CSP safeguards.
Mocked Web tests only, no live host operations or deployment.
