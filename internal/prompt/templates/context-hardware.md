# Hardware

Credentials are never included in this file.
{{range .HardwareGroups}}
## {{.ID}}
- description: {{.Description}}
- access: {{.Access}}
- mode: {{.Mode}}
{{if .HasSerial}}- serial: {{.SerialDevice}} @ {{.SerialBaudrate}}
{{end}}{{if .HasNetwork}}- network: {{.NetworkHost}}:{{.NetworkPort}} ({{.NetworkProtocol}})
{{end}}{{if .SSHAuthentication}}- ssh authentication: {{.SSHAuthentication}}
{{end}}{{if .Username}}- username: {{.Username}}
{{end}}- password configured: {{.PasswordConfigured}}
{{end}}
