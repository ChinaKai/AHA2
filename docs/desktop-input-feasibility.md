# Independent Host Input Feasibility

## Scope

Owner requests a shared host workspace for arbitrary applications, without a
VM, without taking over their physical input, and without requiring a dedicated
launcher/adapter for every application. This investigation does not authorize
installation of drivers, additional user accounts, session-service changes,
privilege elevation, input injection or deployment.

## Result

The tested, supported Win32 desktop mechanisms do not provide a second
concurrently active input desktop inside the current interactive window station.
Creating a window/desktop and streaming its image is not equivalent to creating
an independent, general-purpose input session.

This is a bounded engineering conclusion, not a claim that no third-party
software, custom virtualization layer, different OS configuration or future
Windows feature could ever do so. None of those alternatives was installed or
validated here.

### Virtual Desktops

`IVirtualDesktopManager` groups and moves top-level windows between virtual
workspaces. It does not expose a second input device/session. Reorganizing the
same user's windows does not supply the missing input isolation.

### Win32 Desktop Objects

The Microsoft `Desktops` documentation specifies that only one desktop of the
interactive window station is active at a time. That is the visible input
desktop. `SwitchDesktop` selects a different input desktop; it is not a way to
keep the user's original desktop active while another receives its own ordinary
input.

`GetCursorPos` requires the calling thread's desktop to be the input desktop.
`SendInput` inserts events into the keyboard/mouse input stream and has no target
desktop/window parameter. No input-injection experiment was performed, because
that would risk violating the Owner's input requirement.

### Independent Login Sessions

Concurrent interactive sessions are a different mechanism from virtual desktops
or `CreateDesktop`. Microsoft documents multi-session hosting in the Windows
Server / Windows Enterprise multi-session context. This is not evidence that an
ordinary Windows 11 Pro AHA process can create a second parallel input session
with its existing privileges. Such an environment would require a separate
system-support, permission and deployment assessment, not a UI button.

## Local Probe

Observed September 14, 2026 on Windows 11 Pro, version 10.0.26200, without
administrator elevation.

The workspace-local probe is
`.data/shared-control-test/probe-input-desktop.ps1`, with results in
`.data/shared-control-test/input-desktop-probe-result.json`.
It uses a fresh worker thread to create a new desktop, attach to it, create an
invisible empty test window, query desktop/cursor state, and clean up.

| Check | Result |
| --- | --- |
| Original thread belongs to input desktop | true |
| Original cursor query succeeds | true |
| Create another desktop | true |
| Attach worker to it | true |
| Create own test window there | true |
| Worker's desktop differs from input desktop | true |
| Inactive desktop cursor query succeeds | false |
| Cursor query Win32 error | 5 |
| Input desktop and foreground window unchanged | true |
| Worker restored, own window destroyed, desktop closed | true |
| Keyboard/mouse events injected | false |
| Desktop switched | false |

The cursor failure alone is not proof that every conceivable background-control
method is impossible. Together with the documented input-desktop model, it
demonstrates why creating a desktop is insufficient for the proposed ordinary
Win32 input solution. Existing application-specific automation can still work,
but retains application/control compatibility constraints.

The first file invocation was blocked by the PowerShell file execution policy.
The successful diagnostic invocation used a process-local execution-policy
option; no persistent policy, ACL, administrator identity, account or service
configuration was changed.

## Implications For AHA

- The deployed UIA/native-control implementation is limited control automation,
  not a general desktop-input session. Its Edge actions-empty result remains
  unresolved.
- A unified launcher can simplify presentation but cannot remove the need for
  compatible underlying application control.
- General foreground input would require the user to yield input during control,
  which the current requirement does not permit.
- True simultaneous input requires a supported independent interactive-session
  environment or another separately assessed isolation mechanism. Do not
  silently substitute it for the requested current-host solution.
- No product code or deployment was changed by this investigation.

## Primary References

Microsoft Learn, verified September 14, 2026:

```text
Desktops
https://learn.microsoft.com/en-us/windows/win32/winstation/desktops

GetCursorPos
https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getcursorpos

SendInput
https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-sendinput

IVirtualDesktopManager
https://learn.microsoft.com/en-us/windows/win32/api/shobjidl_core/nn-shobjidl_core-ivirtualdesktopmanager

Windows Enterprise multi-session FAQ
https://learn.microsoft.com/en-us/azure/virtual-desktop/windows-multisession-faq
```
