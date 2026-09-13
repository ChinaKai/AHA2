# Turn Diagnostics{{range .TurnDiagnostics}}
- Turn {{.Sequence}} {{.AgentID}} [{{.Status}}, attempt {{.Attempt}}, generation {{.Generation}}]: {{.Body}}{{end}}
