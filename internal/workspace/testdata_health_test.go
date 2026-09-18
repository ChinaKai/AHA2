package workspace

// aha2HealthPayload is the exact body a real AHA2 instance returns from /healthz.
// Tests that stand in for AHA must return this, because both the discovery probe
// and the transport's own forward verification check for it: any other body is
// correctly rejected as "not AHA2", which would make such a test prove nothing
// about the path it is meant to exercise.
const aha2HealthPayload = `{"ok":true,"service":"aha2"}`
