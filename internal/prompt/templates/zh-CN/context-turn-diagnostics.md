# Turn 诊断{{range .TurnDiagnostics}}
- Turn {{.Sequence}} {{.AgentID}} [{{.Status}}，attempt {{.Attempt}}，generation {{.Generation}}]：{{.Body}}{{end}}
