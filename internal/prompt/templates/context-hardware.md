# Hardware

Credentials are never included in this file.
{{range .HardwareGroups}}
## {{.ID}}
- description: {{.Description}}
- access: {{.Access}}
- mode: {{.Mode}}
{{if .HasSerial}}- serial: {{.SerialDevice}} @ {{.SerialBaudrate}}
{{end}}{{if .HasNetwork}}{{if .ADBUSB}}- network: ADB USB direct, device serial {{.ADBSerial}}
{{else}}- network: {{.NetworkHost}}:{{.NetworkPort}} ({{.NetworkProtocol}}{{if eq .ADBConnection "tcp"}}, network direct without pairing{{else if eq .ADBConnection "wireless"}}, Android wireless debugging{{end}})
{{end}}{{end}}{{if .SSHAuthentication}}- ssh authentication: {{.SSHAuthentication}}
{{end}}{{if .Username}}- username: {{.Username}}
{{end}}- password configured: {{.PasswordConfigured}}
{{end}}
