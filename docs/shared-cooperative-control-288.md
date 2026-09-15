# Cooperative Shared Control (Inbox288)

Owner reports the previous desktop issue no longer reproduces, and requests
shared Agent/Owner access without exclusive control handoffs. Implement and
verify only; no new deployment or commit requested this round.

## Protocol (Main)

New Web sharing/switching sends confirm_shared:true as explicit consent that
both the active Main Agent and Owner may observe/control the selected target.
Foreground confirmation still covers host mouse/keyboard/focus and desktop
switch effects; background sharing also confirms shared access.
The server advertises status.shared_control_supported=true.

An opted-in new session has controller:"shared", revision and target scope as
before. Owner and Agent use their own observations and permissions concurrently.
Only Owner may choose/switch/stop targets. Agent endpoints require the current
Task Main Turn capability. Shared Agent observation/action requests register a
Turn-bound notice automatically through the existing Claim logic, without
changing controller/revision or closing the Owner stream.

Legacy requests lacking confirm_shared retain the old exclusive-controller
semantics. Never silently broaden an old Owner-only grant. Old sessions must be
stopped/re-shared through the new explicit consent before cooperative access.
The exclusive /control operation rejects shared sessions without mutating them;
Agent claim remains valid but is not a control transfer.

Shared native input remains single-flight. Another request during an action
receives busy before dispatch (no hidden queue/replay of uncertain input).
When one participant acts, invalidate other participants' old frames, including
other Owner views; the other side must observe again before issuing input.
Do not reset the session, controller or stream for a successful helper action.
Stop/switch/expiry/native failure revoke old input/frames as before.
On shared native failure, retain shared permission but require new observations;
never auto-replay the failed input. Existing native trust/quarantine rules remain.

## Web Assignment (Sub-002)

Own web/src/desktop_panel.ts, task_tools.ts if needed, CSS if needed and focused
Web tests. No Go/native/HTTP/Prompt editing. No live desktop or deployment.

Remove Owner/Agent handoff buttons and listeners. Shared status is cooperative,
not an exclusive role. Shared Owner controls remain usable when Agent has
claimed the session; observation/control_error/busy/revocation checks remain.
Agent needs no click on a robot icon. Keep Stop and restore focus prominent.
Retain persistent quality and keyboard/text rows, desktop labels, tooltips and
all listener/CSP/readonly/draft/gesture protections.

Share/switch must send confirm_shared:true and explicitly confirm joint access.
When status.shared_control_supported is absent/false, fail closed with a clear
incompatibility message rather than sending an unsupported payload. Legacy
exclusive sessions may still be observed/stopped; explain re-sharing in status,
do not silently enable joint access or keep hidden transfer controls.
Safe error code desktop_shared_control: shared sessions do not transfer control.

Tests: shared initial/claimed states permit Owner assistance without /control;
sharing confirms once; selected target/switch revokes prior input; unsupported
server does not create shared grants; old sessions never auto-upgrade; busy and
stale-observation paths never replay; readonly/error frames remain nonactionable;
DOM lifecycle/drafts and mobile/narrow layouts remain stable.

## Verification

Main owns backend contracts, capability/consent/role/claim tests, concurrent input
safety and native test adaptation. Sub-002 returns Web verification through AHA.
No need to reproduce the resolved desktop error or manipulate production 8766.
Current runtime grants continue to follow the active Agent API rules until an
Owner-authorized update deploys this new product behavior.
