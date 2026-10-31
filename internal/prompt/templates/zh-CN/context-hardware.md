# 硬件

本文件不包含凭据。
{{range .HardwareGroups}}
## {{.ID}}
- 描述：{{.Description}}
- 访问方式：{{.Access}}
- 模式：{{.Mode}}
{{if .HasSerial}}- 串口：{{.SerialDevice}} @ {{.SerialBaudrate}}
{{end}}{{if .HasNetwork}}- 网络：{{.NetworkHost}}:{{.NetworkPort}}（{{.NetworkProtocol}}）
{{end}}{{if .SSHAuthentication}}- SSH 认证：{{.SSHAuthentication}}
{{end}}{{if .Username}}- 用户名：{{.Username}}
{{end}}- 已配置密码：{{.PasswordConfigured}}
{{end}}
