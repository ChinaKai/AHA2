# task-726 review packet diff-1-go-core：Go 生产代码：domain / hardwareconfig / hardware / store

`git diff 43624a4 3890244 -- internal/domain/hardware.go internal/hardwareconfig/config.go internal/hardware/adb.go internal/hardware/adb_usb.go internal/hardware/manager.go internal/store/hardware.go internal/store/migrations.go` 原样输出。

~~~~diff
diff --git a/internal/domain/hardware.go b/internal/domain/hardware.go
index b542b17..fdc0c40 100644
--- a/internal/domain/hardware.go
+++ b/internal/domain/hardware.go
@@ -1,6 +1,10 @@
 package domain
 
-import "time"
+import (
+	"regexp"
+	"strings"
+	"time"
+)
 
 const (
 	HardwareModeOff     = "off"
@@ -22,6 +26,18 @@ const (
 	HardwareSSHAuthAuto     = "auto"
 	HardwareSSHAuthPassword = "password"
 	HardwareSSHAuthKey      = "key"
+
+	// ADB connection forms. All use network.protocol=adb; the form decides
+	// which target fields apply. An empty value is the pre-split stored shape
+	// and always means wireless, so existing paired phones keep their meaning.
+	//   wireless: Android wireless debugging, host + connection port, optional
+	//             pairing with a separate pairing port and six-digit code.
+	//   tcp:      classic network ADB (adb tcpip), explicit host:port
+	//             (default 5555), no pairing and no mDNS port discovery.
+	//   usb:      device attached to the AHA host, addressed by serial only.
+	HardwareADBConnectionWireless = "wireless"
+	HardwareADBConnectionTCP      = "tcp"
+	HardwareADBConnectionUSB      = "usb"
 )
 
 type HardwareSerialConfig struct {
@@ -34,6 +50,76 @@ type HardwareNetworkConfig struct {
 	Port     int    `json:"port"`
 	Protocol string `json:"protocol"`
 	SSHAuth  string `json:"ssh_auth"`
+	// ADBConnection is wireless (pairing code + host:port), tcp (explicit
+	// host:port, no pairing) or usb (device serial on the AHA host, no
+	// host/port). Only meaningful for protocol=adb.
+	ADBConnection string `json:"adb_connection,omitempty"`
+	// ADBSerial is the USB device serial for ADBConnection=usb.
+	ADBSerial string `json:"adb_serial,omitempty"`
+}
+
+// ADBUSB reports whether the config targets a USB-attached ADB device by serial.
+func (config HardwareNetworkConfig) ADBUSB() bool {
+	return config.Protocol == HardwareProtocolADB && config.ADBConnection == HardwareADBConnectionUSB
+}
+
+// ADBTCP reports whether the config is a classic network ADB target: the
+// explicit host:port is connected as is, without pairing or mDNS discovery.
+func (config HardwareNetworkConfig) ADBTCP() bool {
+	return config.Protocol == HardwareProtocolADB && config.ADBConnection == HardwareADBConnectionTCP
+}
+
+// ADBWireless reports whether the config is an ADB wireless-debugging target
+// (host:port, optional pairing). Legacy rows without a connection form count.
+func (config HardwareNetworkConfig) ADBWireless() bool {
+	return config.Protocol == HardwareProtocolADB &&
+		config.ADBConnection != HardwareADBConnectionUSB && config.ADBConnection != HardwareADBConnectionTCP
+}
+
+// NormalizedADB fills the explicit connection form for ADB rows stored before
+// the USB/wireless split and clears ADB-only fields for other protocols. It
+// never changes host/port, so a legacy paired phone stays wireless.
+func (config HardwareNetworkConfig) NormalizedADB() HardwareNetworkConfig {
+	if config.Protocol != HardwareProtocolADB {
+		config.ADBConnection = ""
+		config.ADBSerial = ""
+		return config
+	}
+	switch config.ADBConnection {
+	case HardwareADBConnectionUSB:
+	case HardwareADBConnectionTCP:
+		config.ADBSerial = ""
+	default:
+		config.ADBConnection = HardwareADBConnectionWireless
+		config.ADBSerial = ""
+	}
+	return config
+}
+
+// hasNetworkTarget reports whether the network side has a usable target:
+// a USB serial for ADB USB, otherwise a host.
+func (config HardwareNetworkConfig) hasNetworkTarget() bool {
+	if config.ADBUSB() {
+		return config.ADBSerial != ""
+	}
+	return config.Host != ""
+}
+
+var adbUSBSerialPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
+
+// ValidADBUSBSerial accepts only serials that adb reports for USB devices.
+// Network transports (host:port), mDNS service names and emulators are
+// rejected so a USB resource can never be pointed at a network device, and
+// adb -s device qualifiers (model:, usb:, product:) cannot be smuggled in.
+func ValidADBUSBSerial(serial string) bool {
+	if !adbUSBSerialPattern.MatchString(serial) {
+		return false
+	}
+	lower := strings.ToLower(serial)
+	if strings.HasPrefix(lower, "emulator-") || strings.Contains(lower, "._adb") || strings.HasSuffix(lower, "._tcp") {
+		return false
+	}
+	return true
 }
 
 type HardwareResource struct {
@@ -66,7 +152,7 @@ func (item HardwareResource) Supports(transport string) bool {
 	case HardwareTransportSerial:
 		return (item.Mode == HardwareModeSerial || item.Mode == HardwareModeBoth) && item.Serial.Device != ""
 	case HardwareTransportNetwork:
-		return (item.Mode == HardwareModeNetwork || item.Mode == HardwareModeBoth) && item.Network.Host != ""
+		return (item.Mode == HardwareModeNetwork || item.Mode == HardwareModeBoth) && item.Network.hasNetworkTarget()
 	default:
 		return false
 	}
@@ -94,7 +180,7 @@ func (item HardwareGroup) Supports(transport string) bool {
 	case HardwareTransportSerial:
 		return (item.Mode == HardwareModeSerial || item.Mode == HardwareModeBoth) && item.Serial.Device != ""
 	case HardwareTransportNetwork:
-		return (item.Mode == HardwareModeNetwork || item.Mode == HardwareModeBoth) && item.Network.Host != ""
+		return (item.Mode == HardwareModeNetwork || item.Mode == HardwareModeBoth) && item.Network.hasNetworkTarget()
 	default:
 		return false
 	}
diff --git a/internal/hardware/adb.go b/internal/hardware/adb.go
index 0a5a382..3e43500 100644
--- a/internal/hardware/adb.go
+++ b/internal/hardware/adb.go
@@ -69,7 +69,43 @@ func executeADBWithExecutable(ctx context.Context, executable, endpoint string,
 	if err != nil {
 		return ADBExecResult{}, err
 	}
-	commandArgs := append([]string{"-s", resolvedEndpoint}, arguments...)
+	return runADBDeviceCommand(commandCtx, executable, resolvedEndpoint, arguments, input, timeout, startedAt)
+}
+
+// ExecuteADBTCP runs one device command on a classic network ADB target
+// (explicit host:port, no pairing, no mDNS discovery).
+func ExecuteADBTCP(ctx context.Context, endpoint string, arguments []string, input []byte, timeout time.Duration) (ADBExecResult, error) {
+	if len(arguments) == 0 {
+		return ADBExecResult{}, fmt.Errorf("ADB command arguments are required")
+	}
+	if timeout <= 0 {
+		timeout = 30 * time.Second
+	}
+	if timeout > maxADBExecTimeout {
+		timeout = maxADBExecTimeout
+	}
+	executable, err := resolveADBExecutable()
+	if err != nil {
+		return ADBExecResult{}, err
+	}
+	return executeADBTCPWithExecutable(ctx, executable, endpoint, arguments, input, timeout)
+}
+
+func executeADBTCPWithExecutable(ctx context.Context, executable, endpoint string, arguments []string, input []byte, timeout time.Duration) (ADBExecResult, error) {
+	commandCtx, cancel := context.WithTimeout(ctx, timeout)
+	defer cancel()
+	startedAt := time.Now()
+	target, err := connectADBDirect(commandCtx, executable, endpoint)
+	if err != nil {
+		return ADBExecResult{}, err
+	}
+	return runADBDeviceCommand(commandCtx, executable, target, arguments, input, timeout, startedAt)
+}
+
+// runADBDeviceCommand runs one device command pinned with -s to an already
+// verified target: a wireless host:port after connect, or a USB serial.
+func runADBDeviceCommand(commandCtx context.Context, executable, target string, arguments []string, input []byte, timeout time.Duration, startedAt time.Time) (ADBExecResult, error) {
+	commandArgs := append([]string{"-s", target}, arguments...)
 	command := exec.CommandContext(commandCtx, executable, commandArgs...)
 	command.Stdin = bytes.NewReader(input)
 	stdout := &limitedADBWriter{limit: MaxADBExecOutputBytes}
@@ -77,9 +113,9 @@ func executeADBWithExecutable(ctx context.Context, executable, endpoint string,
 	command.Stdout = stdout
 	command.Stderr = stderr
 	configureADBCommand(command)
-	err = command.Run()
+	err := command.Run()
 	result := ADBExecResult{
-		Endpoint: resolvedEndpoint,
+		Endpoint: target,
 		ExitCode: 0,
 		Stdout:   stdout.Bytes(),
 		Stderr:   stderr.Bytes(),
@@ -186,8 +222,31 @@ func openADBTerminal(ctx context.Context, endpoint string) (io.ReadWriteCloser,
 	if err != nil {
 		return nil, err
 	}
+	return startADBShell(executable, resolvedEndpoint, resolvedEndpoint)
+}
+
+func openADBTCPTerminal(ctx context.Context, endpoint string) (io.ReadWriteCloser, error) {
+	executable, err := resolveADBExecutable()
+	if err != nil {
+		return nil, err
+	}
+	return openADBTCPTerminalWithExecutable(ctx, executable, endpoint)
+}
+
+func openADBTCPTerminalWithExecutable(ctx context.Context, executable, endpoint string) (io.ReadWriteCloser, error) {
+	connectCtx, cancel := context.WithTimeout(ctx, adbAuthorizeWait+15*time.Second)
+	defer cancel()
+	target, err := connectADBDirect(connectCtx, executable, endpoint)
+	if err != nil {
+		return nil, err
+	}
+	return startADBShell(executable, target, target)
+}
 
-	command := exec.Command(executable, "-s", resolvedEndpoint, "shell", "-t", "-t")
+// startADBShell opens an interactive shell pinned with -s to a verified target.
+// display is what the terminal reports as its endpoint.
+func startADBShell(executable, target, display string) (io.ReadWriteCloser, error) {
+	command := exec.Command(executable, "-s", target, "shell", "-t", "-t")
 	configureADBCommand(command)
 	stdin, err := command.StdinPipe()
 	if err != nil {
@@ -208,7 +267,7 @@ func openADBTerminal(ctx context.Context, endpoint string) (io.ReadWriteCloser,
 		output:   terminalOutput,
 		writer:   writer,
 		done:     make(chan struct{}),
-		endpoint: resolvedEndpoint,
+		endpoint: display,
 	}
 	go func() {
 		waitErr := command.Wait()
@@ -222,11 +281,27 @@ func openADBTerminal(ctx context.Context, endpoint string) (io.ReadWriteCloser,
 	return terminal, nil
 }
 
+// connectADB is the Android wireless-debugging path: mDNS may correct the
+// connection port, and an unauthorized device gets one adb server restart.
 func connectADB(ctx context.Context, executable, endpoint string) (string, error) {
 	resolvedEndpoint, err := resolveADBMdnsEndpoint(ctx, executable, endpoint)
 	if err != nil {
 		return "", err
 	}
+	return connectADBEndpoint(ctx, executable, resolvedEndpoint, true)
+}
+
+// connectADBDirect is classic network ADB: the configured host:port is used
+// exactly as is. No mDNS lookup can swap it for another port or device, no
+// pairing is attempted, and the shared adb server is never restarted.
+func connectADBDirect(ctx context.Context, executable, endpoint string) (string, error) {
+	if _, _, err := net.SplitHostPort(endpoint); err != nil {
+		return "", fmt.Errorf("ADB 网络直连地址无效：%s", endpoint)
+	}
+	return connectADBEndpoint(ctx, executable, endpoint, false)
+}
+
+func connectADBEndpoint(ctx context.Context, executable, resolvedEndpoint string, restartOnUnauthorized bool) (string, error) {
 	if err := dialADB(ctx, executable, resolvedEndpoint); err != nil {
 		return "", err
 	}
@@ -234,7 +309,7 @@ func connectADB(ctx context.Context, executable, endpoint string) (string, error
 	if err != nil {
 		return "", err
 	}
-	if state == "unauthorized" {
+	if state == "unauthorized" && restartOnUnauthorized {
 		// Android re-prompts only for a fresh server session: once a key was
 		// denied (or the stored key changed) the dialog stays away until the
 		// daemon restarts. A TV that "never shows the prompt" is almost always
@@ -255,6 +330,9 @@ func connectADB(ctx context.Context, executable, endpoint string) (string, error
 	case "offline":
 		return "", fmt.Errorf("设备处于离线状态（%s）：请在设备上重新确认授权，或关闭再打开该设备的网络调试后重试。", resolvedEndpoint)
 	case "unauthorized":
+		if !restartOnUnauthorized {
+			return "", fmt.Errorf("设备未授权（%s）：请在设备上确认「允许 USB 调试」弹窗（建议勾选一律允许）后重试；若始终不弹窗，请在设备开发者选项里「撤销 USB 调试授权」后重新开启网络 ADB。", resolvedEndpoint)
+		}
 		return "", fmt.Errorf("设备未授权（%s）：请在电视/手机上确认「允许 USB 调试」弹窗（建议勾选一律允许）。已自动重启 ADB 服务重试仍未授权；若电视始终不弹窗，请在电视开发者选项里「撤销 USB 调试授权」，或在电视上关闭再打开 ADB 调试后重试。", resolvedEndpoint)
 	default:
 		return "", fmt.Errorf("ADB 设备状态异常（%s）：%s", resolvedEndpoint, state)
diff --git a/internal/hardware/adb_usb.go b/internal/hardware/adb_usb.go
new file mode 100644
index 0000000..20c8209
--- /dev/null
+++ b/internal/hardware/adb_usb.go
@@ -0,0 +1,269 @@
+package hardware
+
+import (
+	"context"
+	"fmt"
+	"io"
+	"os/exec"
+	"strings"
+	"time"
+	"unicode"
+
+	"github.com/ChinaKai/AHA2/internal/domain"
+)
+
+// ADB USB direct: the device is attached to the machine running AHA and is
+// addressed only by its serial. Nothing here runs adb pair, adb connect or
+// mDNS discovery, and nothing restarts the shared adb server, so a USB target
+// can never be redirected to a network device or disturb other devices.
+
+// ADBUSBDevice is one USB-attached device reported by `adb devices -l` on the
+// AHA host. Fields are external data and are sanitized before leaving here.
+type ADBUSBDevice struct {
+	Serial     string `json:"serial"`
+	State      string `json:"state"`
+	Model      string `json:"model,omitempty"`
+	Product    string `json:"product,omitempty"`
+	Device     string `json:"device,omitempty"`
+	USB        string `json:"usb,omitempty"`
+	Selectable bool   `json:"selectable"`
+}
+
+// ADBUSBDeviceList is the enumeration result. OtherCount counts network,
+// mDNS and emulator transports that were deliberately left out.
+type ADBUSBDeviceList struct {
+	Devices    []ADBUSBDevice `json:"devices"`
+	OtherCount int            `json:"other_count"`
+}
+
+// USBDisplayEndpoint is how a USB target is shown in terminal status.
+func USBDisplayEndpoint(serial string) string {
+	return "USB " + serial
+}
+
+// ListADBUSBDevices enumerates USB devices on the AHA host. It is an Owner
+// resource-configuration capability; Task Agents never reach it.
+func ListADBUSBDevices(ctx context.Context) (ADBUSBDeviceList, error) {
+	executable, err := resolveADBExecutable()
+	if err != nil {
+		return ADBUSBDeviceList{}, err
+	}
+	return listADBUSBDevicesWithExecutable(ctx, executable)
+}
+
+func listADBUSBDevicesWithExecutable(ctx context.Context, executable string) (ADBUSBDeviceList, error) {
+	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
+	defer cancel()
+	command := exec.CommandContext(listCtx, executable, "devices", "-l")
+	configureADBCommand(command)
+	output, err := command.CombinedOutput()
+	if err != nil {
+		return ADBUSBDeviceList{}, fmt.Errorf("adb devices: %s", adbFailure(sanitizeADBText(string(output), 512), err))
+	}
+	return parseADBDevices(string(output)), nil
+}
+
+// parseADBDevices reads `adb devices -l`. Network transports (host:port),
+// mDNS service names and emulators are counted but not listed, so the Owner
+// can only pick a USB device for a USB resource.
+func parseADBDevices(output string) ADBUSBDeviceList {
+	result := ADBUSBDeviceList{Devices: []ADBUSBDevice{}}
+	seen := map[string]int{}
+	for _, line := range strings.Split(output, "\n") {
+		line = strings.TrimSpace(line)
+		if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(strings.ToLower(line), "list of devices") {
+			continue
+		}
+		fields := strings.Fields(line)
+		if len(fields) < 2 {
+			continue
+		}
+		serial := fields[0]
+		rest := fields[1:]
+		state := rest[0]
+		rest = rest[1:]
+		if state == "no" && len(rest) > 0 && rest[0] == "permissions" {
+			state = "no_permissions"
+			rest = rest[1:]
+			// Skip the parenthesised hint adb prints after "no permissions".
+			if len(rest) > 0 && strings.HasPrefix(rest[0], "(") {
+				for len(rest) > 0 {
+					word := rest[0]
+					rest = rest[1:]
+					if strings.HasSuffix(word, ")") || strings.HasSuffix(word, ");") {
+						break
+					}
+				}
+			}
+		}
+		attributes := map[string]string{}
+		for _, field := range rest {
+			key, value, ok := strings.Cut(field, ":")
+			if ok {
+				attributes[key] = value
+			}
+		}
+		if adbNetworkSerial(serial) {
+			result.OtherCount++
+			continue
+		}
+		device := ADBUSBDevice{
+			Serial:     sanitizeADBText(serial, 128),
+			State:      sanitizeADBText(state, 32),
+			Model:      sanitizeADBText(attributes["model"], 128),
+			Product:    sanitizeADBText(attributes["product"], 128),
+			Device:     sanitizeADBText(attributes["device"], 128),
+			USB:        sanitizeADBText(attributes["usb"], 64),
+			Selectable: domain.ValidADBUSBSerial(serial),
+		}
+		if index, duplicate := seen[serial]; duplicate {
+			// Two devices sharing one serial cannot be told apart by adb -s;
+			// neither may be selected.
+			result.Devices[index].Selectable = false
+			device.Selectable = false
+		} else {
+			seen[serial] = len(result.Devices)
+		}
+		result.Devices = append(result.Devices, device)
+	}
+	return result
+}
+
+func adbNetworkSerial(serial string) bool {
+	lower := strings.ToLower(serial)
+	return strings.Contains(serial, ":") || strings.HasPrefix(lower, "emulator-") ||
+		strings.Contains(lower, "._adb") || strings.HasSuffix(lower, "._tcp")
+}
+
+func sanitizeADBText(value string, limit int) string {
+	value = strings.Map(func(r rune) rune {
+		if unicode.IsControl(r) {
+			return -1
+		}
+		return r
+	}, strings.TrimSpace(value))
+	if len(value) > limit {
+		value = value[:limit]
+	}
+	return value
+}
+
+// ExecuteADBUSB runs one device command on the USB device with this serial.
+func ExecuteADBUSB(ctx context.Context, serial string, arguments []string, input []byte, timeout time.Duration) (ADBExecResult, error) {
+	if len(arguments) == 0 {
+		return ADBExecResult{}, fmt.Errorf("ADB command arguments are required")
+	}
+	if !domain.ValidADBUSBSerial(serial) {
+		return ADBExecResult{}, fmt.Errorf("USB 设备序列号无效")
+	}
+	if timeout <= 0 {
+		timeout = 30 * time.Second
+	}
+	if timeout > maxADBExecTimeout {
+		timeout = maxADBExecTimeout
+	}
+	executable, err := resolveADBExecutable()
+	if err != nil {
+		return ADBExecResult{}, err
+	}
+	return executeADBUSBWithExecutable(ctx, executable, serial, arguments, input, timeout)
+}
+
+func executeADBUSBWithExecutable(ctx context.Context, executable, serial string, arguments []string, input []byte, timeout time.Duration) (ADBExecResult, error) {
+	commandCtx, cancel := context.WithTimeout(ctx, timeout)
+	defer cancel()
+	startedAt := time.Now()
+	if err := ensureADBUSBDevice(commandCtx, executable, serial); err != nil {
+		return ADBExecResult{}, err
+	}
+	return runADBDeviceCommand(commandCtx, executable, serial, arguments, input, timeout, startedAt)
+}
+
+func openADBUSBTerminal(ctx context.Context, serial string) (io.ReadWriteCloser, error) {
+	if !domain.ValidADBUSBSerial(serial) {
+		return nil, fmt.Errorf("USB 设备序列号无效")
+	}
+	executable, err := resolveADBExecutable()
+	if err != nil {
+		return nil, err
+	}
+	return openADBUSBTerminalWithExecutable(ctx, executable, serial)
+}
+
+func openADBUSBTerminalWithExecutable(ctx context.Context, executable, serial string) (io.ReadWriteCloser, error) {
+	checkCtx, cancel := context.WithTimeout(ctx, adbAuthorizeWait+15*time.Second)
+	defer cancel()
+	if err := ensureADBUSBDevice(checkCtx, executable, serial); err != nil {
+		return nil, err
+	}
+	return startADBShell(executable, serial, USBDisplayEndpoint(serial))
+}
+
+// ensureADBUSBDevice confirms the serial is attached and authorized. An
+// unauthorized device gets one window for the Owner to accept the prompt on
+// the device; nothing here accepts or bypasses that prompt.
+func ensureADBUSBDevice(ctx context.Context, executable, serial string) error {
+	deadline := time.Now().Add(adbAuthorizeWait)
+	for {
+		state, detail := adbUSBDeviceState(ctx, executable, serial)
+		switch state {
+		case "device":
+			return nil
+		case "missing":
+			return fmt.Errorf("未在 AHA 宿主机上找到 USB 设备 %s：请确认数据线已连接、设备已开启 USB 调试，并在硬件资源页刷新设备列表", serial)
+		case "ambiguous":
+			return fmt.Errorf("AHA 宿主机上有多台设备使用同一序列号 %s，无法确定目标：请只保留其中一台后重试", serial)
+		case "offline":
+			return fmt.Errorf("USB 设备 %s 处于离线状态：请重新插拔数据线，或在设备上关闭再打开 USB 调试后重试", serial)
+		case "no_permissions":
+			return fmt.Errorf("AHA 宿主机没有访问 USB 设备 %s 的权限：请检查宿主机 USB 驱动或权限设置", serial)
+		case "unauthorized":
+			if time.Now().After(deadline) {
+				return fmt.Errorf("USB 设备 %s 未授权：请在设备上确认「允许 USB 调试」弹窗（建议勾选一律允许）后重试", serial)
+			}
+		case "error":
+			return fmt.Errorf("读取 USB 设备 %s 状态失败：%s", serial, detail)
+		default:
+			return fmt.Errorf("USB 设备 %s 状态异常：%s", serial, state)
+		}
+		select {
+		case <-ctx.Done():
+			return ctx.Err()
+		case <-time.After(adbAuthorizePollInterval):
+		}
+	}
+}
+
+// adbUSBDeviceState maps `adb -s <serial> get-state` to device, unauthorized,
+// offline, no_permissions, missing, ambiguous, error or another raw state.
+func adbUSBDeviceState(ctx context.Context, executable, serial string) (string, string) {
+	stateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
+	defer cancel()
+	command := exec.CommandContext(stateCtx, executable, "-s", serial, "get-state")
+	configureADBCommand(command)
+	output, err := command.CombinedOutput()
+	message := strings.ToLower(strings.TrimSpace(string(output)))
+	if err == nil {
+		state := sanitizeADBText(message, 32)
+		if state == "" {
+			return "error", "adb 未返回设备状态"
+		}
+		return state, ""
+	}
+	switch {
+	case strings.Contains(message, "more than one"):
+		return "ambiguous", ""
+	case strings.Contains(message, "not found"), strings.Contains(message, "no devices"):
+		return "missing", ""
+	case strings.Contains(message, "unauthorized"):
+		return "unauthorized", ""
+	case strings.Contains(message, "no permissions"), strings.Contains(message, "insufficient permissions"):
+		return "no_permissions", ""
+	case strings.Contains(message, "offline"):
+		return "offline", ""
+	}
+	if ctx.Err() != nil {
+		return "error", ctx.Err().Error()
+	}
+	return "error", adbFailure(sanitizeADBText(string(output), 512), err)
+}
diff --git a/internal/hardware/manager.go b/internal/hardware/manager.go
index a1079f2..fb2bbcc 100644
--- a/internal/hardware/manager.go
+++ b/internal/hardware/manager.go
@@ -120,6 +120,12 @@ type Manager struct {
 	dialNetwork func(context.Context, string, string) (net.Conn, error)
 	openSSH     func(context.Context, string, string, string, string) (io.ReadWriteCloser, error)
 	openADB     func(context.Context, string) (io.ReadWriteCloser, error)
+	// openADBUSB opens a shell on a USB-attached device by serial. It is kept
+	// separate from openADB so a USB target never goes through adb connect.
+	openADBUSB func(context.Context, string) (io.ReadWriteCloser, error)
+	// openADBTCP opens a shell on a classic network ADB host:port without
+	// pairing or mDNS port discovery.
+	openADBTCP func(context.Context, string) (io.ReadWriteCloser, error)
 
 	mu          sync.Mutex
 	sessions    map[string]*terminalSession
@@ -138,6 +144,8 @@ func NewManager(recorder Recorder) *Manager {
 		},
 		openSSH:     openSSHTerminal,
 		openADB:     openADBTerminal,
+		openADBUSB:  openADBUSBTerminal,
+		openADBTCP:  openADBTCPTerminal,
 		sessions:    map[string]*terminalSession{},
 		attachments: map[string]*terminalSession{},
 	}
@@ -206,6 +214,10 @@ func (manager *Manager) Connect(ctx context.Context, request ConnectRequest) (do
 		connection, err = manager.openSerial(request.Group.Serial.Device, baudrate)
 	} else if protocol == domain.HardwareProtocolSSH {
 		connection, err = manager.openSSH(ctx, endpoint, request.Group.Username, request.Password, sshAuth)
+	} else if protocol == domain.HardwareProtocolADB && request.Group.Network.ADBUSB() {
+		connection, err = manager.openADBUSB(ctx, request.Group.Network.ADBSerial)
+	} else if protocol == domain.HardwareProtocolADB && request.Group.Network.ADBTCP() {
+		connection, err = manager.openADBTCP(ctx, endpoint)
 	} else if protocol == domain.HardwareProtocolADB {
 		connection, err = manager.openADB(ctx, endpoint)
 	} else {
@@ -880,6 +892,17 @@ func sessionTarget(group domain.HardwareGroup, transport string) (key, endpoint,
 			err = fmt.Errorf("unsupported network protocol %q", protocol)
 			return
 		}
+		if group.Network.ADBUSB() {
+			// USB direct is addressed by serial only; host and port are unused.
+			serial := strings.TrimSpace(group.Network.ADBSerial)
+			if !domain.ValidADBUSBSerial(serial) {
+				err = fmt.Errorf("ADB USB device serial is required")
+				return
+			}
+			endpoint = USBDisplayEndpoint(serial)
+			key = "network:adb-usb:" + serial
+			return
+		}
 		port := group.Network.Port
 		if port <= 0 {
 			port = defaultNetworkPort(protocol)
diff --git a/internal/hardwareconfig/config.go b/internal/hardwareconfig/config.go
index 96502ec..63e8a76 100644
--- a/internal/hardwareconfig/config.go
+++ b/internal/hardwareconfig/config.go
@@ -102,10 +102,15 @@ func NormalizeGroup(
 	if raw.Network.Port > 65535 {
 		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的网络端口无效", id)
 	}
+	network, err := normalizeADBTarget(id, mode, raw.Network)
+	if err != nil {
+		return domain.HardwareGroup{}, err
+	}
+	raw.Network = network
 	if mode == domain.HardwareModeSerial && raw.Serial.Device == "" {
 		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 缺少串口设备", id)
 	}
-	if mode == domain.HardwareModeNetwork && raw.Network.Host == "" {
+	if mode == domain.HardwareModeNetwork && !raw.Network.ADBUSB() && raw.Network.Host == "" {
 		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 缺少网络地址", id)
 	}
 	if (mode == domain.HardwareModeNetwork || mode == domain.HardwareModeBoth) &&
@@ -113,7 +118,7 @@ func NormalizeGroup(
 		strings.TrimSpace(raw.Username) == "" {
 		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的 SSH 用户名不能为空", id)
 	}
-	if mode == domain.HardwareModeBoth && raw.Serial.Device == "" && raw.Network.Host == "" {
+	if mode == domain.HardwareModeBoth && raw.Serial.Device == "" && raw.Network.Host == "" && raw.Network.ADBSerial == "" {
 		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 至少需要一种连接", id)
 	}
 	createdAt := existing.CreatedAt
@@ -131,3 +136,51 @@ func NormalizeGroup(
 		Access: access, CreatedAt: createdAt, UpdatedAt: now,
 	}, nil
 }
+
+// normalizeADBTarget settles the ADB connection form and keeps only the target
+// fields that form uses. USB direct targets a device serial on the AHA host and
+// never carries a host or port, so it cannot fall back to adb connect or mDNS.
+// Wireless and TCP keep host + port and never carry a serial; only wireless
+// pairs. Non-ADB protocols carry neither ADB field.
+func normalizeADBTarget(id, mode string, network domain.HardwareNetworkConfig) (domain.HardwareNetworkConfig, error) {
+	connection := strings.TrimSpace(network.ADBConnection)
+	serial := strings.TrimSpace(network.ADBSerial)
+	if network.Protocol != domain.HardwareProtocolADB {
+		network.ADBConnection, network.ADBSerial = "", ""
+		return network, nil
+	}
+	switch connection {
+	case "", domain.HardwareADBConnectionWireless:
+		network.ADBConnection = domain.HardwareADBConnectionWireless
+		network.ADBSerial = ""
+		return network, nil
+	case domain.HardwareADBConnectionTCP:
+		// Classic network ADB keeps the explicit host:port (port defaults to
+		// 5555 above) and never carries a serial.
+		network.ADBConnection = domain.HardwareADBConnectionTCP
+		network.ADBSerial = ""
+		return network, nil
+	case domain.HardwareADBConnectionUSB:
+	default:
+		return network, fmt.Errorf("硬件组 %s 的 ADB 连接方式无效", id)
+	}
+	network.ADBConnection = domain.HardwareADBConnectionUSB
+	network.Host, network.Port = "", 0
+	network.ADBSerial = serial
+	if serial != "" && !domain.ValidADBUSBSerial(serial) {
+		return network, fmt.Errorf("硬件组 %s 的 USB 设备序列号无效：只能选择 AHA 宿主机检测到的 USB 设备，不能填写 IP:端口、mDNS 名称或模拟器", id)
+	}
+	if serial == "" && (mode == domain.HardwareModeNetwork || mode == domain.HardwareModeBoth) {
+		return network, fmt.Errorf("硬件组 %s 缺少 USB 设备序列号，请先选择 AHA 宿主机上的 USB 设备", id)
+	}
+	return network, nil
+}
+
+// ADBPairingStillValid reports whether a recorded wireless pairing survives an
+// edit. Pairing belongs to one wireless host: any switch to USB direct, to
+// network direct (tcp), to another protocol or mode, or to another host
+// invalidates it.
+func ADBPairingStillValid(beforeMode string, before domain.HardwareNetworkConfig, afterMode string, after domain.HardwareNetworkConfig) bool {
+	return beforeMode == domain.HardwareModeNetwork && afterMode == domain.HardwareModeNetwork &&
+		before.ADBWireless() && after.ADBWireless() && before.Host == after.Host
+}
diff --git a/internal/store/hardware.go b/internal/store/hardware.go
index 8780a72..eee1824 100644
--- a/internal/store/hardware.go
+++ b/internal/store/hardware.go
@@ -12,7 +12,7 @@ import (
 
 const hardwareResourceColumns = `id,description,mode,serial_device,serial_baudrate,
 	network_host,network_port,network_protocol,ssh_auth,username,credential_ref,
-	password_configured,adb_paired_at,access,created_at,updated_at`
+	password_configured,adb_paired_at,access,created_at,updated_at,adb_connection,adb_serial`
 
 func scanHardwareResource(scanner interface{ Scan(...any) error }) (domain.HardwareResource, error) {
 	var item domain.HardwareResource
@@ -22,7 +22,9 @@ func scanHardwareResource(scanner interface{ Scan(...any) error }) (domain.Hardw
 		&item.ID, &item.Description, &item.Mode, &item.Serial.Device, &item.Serial.Baudrate,
 		&item.Network.Host, &item.Network.Port, &item.Network.Protocol, &item.Network.SSHAuth,
 		&item.Username, &item.CredentialRef, &passwordConfigured, &item.ADBPairedAt, &item.Access, &createdAt, &updatedAt,
+		&item.Network.ADBConnection, &item.Network.ADBSerial,
 	)
+	item.Network = item.Network.NormalizedADB()
 	item.PasswordConfigured = passwordConfigured != 0
 	item.CreatedAt = parseTime(createdAt)
 	item.UpdatedAt = parseTime(updatedAt)
@@ -34,7 +36,7 @@ func (s *Store) HardwareResources(ctx context.Context) ([]domain.HardwareResourc
 		SELECT r.id,r.description,r.mode,r.serial_device,r.serial_baudrate,
 		       r.network_host,r.network_port,r.network_protocol,r.ssh_auth,r.username,
 		       r.credential_ref,r.password_configured,r.adb_paired_at,r.access,r.created_at,r.updated_at,
-		       COUNT(b.task_id)
+		       r.adb_connection,r.adb_serial,COUNT(b.task_id)
 		FROM hardware_resources r
 		LEFT JOIN task_hardware_bindings b ON b.resource_id=r.id
 		GROUP BY r.id
@@ -52,10 +54,11 @@ func (s *Store) HardwareResources(ctx context.Context) ([]domain.HardwareResourc
 			&item.ID, &item.Description, &item.Mode, &item.Serial.Device, &item.Serial.Baudrate,
 			&item.Network.Host, &item.Network.Port, &item.Network.Protocol, &item.Network.SSHAuth,
 			&item.Username, &item.CredentialRef, &passwordConfigured, &item.ADBPairedAt, &item.Access,
-			&createdAt, &updatedAt, &item.BindingCount,
+			&createdAt, &updatedAt, &item.Network.ADBConnection, &item.Network.ADBSerial, &item.BindingCount,
 		); err != nil {
 			return nil, err
 		}
+		item.Network = item.Network.NormalizedADB()
 		item.PasswordConfigured = passwordConfigured != 0
 		item.CreatedAt = parseTime(createdAt)
 		item.UpdatedAt = parseTime(updatedAt)
@@ -100,12 +103,13 @@ func (s *Store) UpsertHardwareResource(ctx context.Context, item domain.Hardware
 	if item.UpdatedAt.IsZero() {
 		item.UpdatedAt = now
 	}
+	network := item.Network.NormalizedADB()
 	_, err := s.db.ExecContext(ctx, `
 		INSERT INTO hardware_resources(
 			id,description,mode,serial_device,serial_baudrate,network_host,network_port,
 			network_protocol,ssh_auth,username,credential_ref,password_configured,adb_paired_at,access,
-			created_at,updated_at
-		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
+			created_at,updated_at,adb_connection,adb_serial
+		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 		ON CONFLICT(id) DO UPDATE SET
 			description=excluded.description,mode=excluded.mode,
 			serial_device=excluded.serial_device,serial_baudrate=excluded.serial_baudrate,
@@ -113,11 +117,11 @@ func (s *Store) UpsertHardwareResource(ctx context.Context, item domain.Hardware
 			network_protocol=excluded.network_protocol,ssh_auth=excluded.ssh_auth,
 			username=excluded.username,credential_ref=excluded.credential_ref,
 			password_configured=excluded.password_configured,adb_paired_at=excluded.adb_paired_at,access=excluded.access,
-			updated_at=excluded.updated_at`,
+			updated_at=excluded.updated_at,adb_connection=excluded.adb_connection,adb_serial=excluded.adb_serial`,
 		item.ID, item.Description, item.Mode, item.Serial.Device, item.Serial.Baudrate,
 		item.Network.Host, item.Network.Port, item.Network.Protocol, item.Network.SSHAuth,
 		item.Username, item.CredentialRef, boolInt(item.PasswordConfigured), item.ADBPairedAt, item.Access,
-		timeString(item.CreatedAt), timeString(item.UpdatedAt),
+		timeString(item.CreatedAt), timeString(item.UpdatedAt), network.ADBConnection, network.ADBSerial,
 	)
 	return err
 }
@@ -232,7 +236,8 @@ func (s *Store) HardwareGroups(ctx context.Context, taskID string) ([]domain.Har
 		SELECT b.task_id,b.id,b.resource_id,b.position,r.description,r.mode,
 		       r.serial_device,r.serial_baudrate,r.network_host,r.network_port,
 		       r.network_protocol,r.ssh_auth,r.username,r.credential_ref,
-		       r.password_configured,r.access,b.created_at,b.updated_at
+		       r.password_configured,r.access,b.created_at,b.updated_at,
+		       r.adb_connection,r.adb_serial
 		FROM task_hardware_bindings b
 		JOIN hardware_resources r ON r.id=b.resource_id
 		WHERE b.task_id=?
@@ -251,9 +256,11 @@ func (s *Store) HardwareGroups(ctx context.Context, taskID string) ([]domain.Har
 			&item.Mode, &item.Serial.Device, &item.Serial.Baudrate, &item.Network.Host,
 			&item.Network.Port, &item.Network.Protocol, &item.Network.SSHAuth, &item.Username,
 			&item.CredentialRef, &passwordConfigured, &item.Access, &createdAt, &updatedAt,
+			&item.Network.ADBConnection, &item.Network.ADBSerial,
 		); err != nil {
 			return nil, err
 		}
+		item.Network = item.Network.NormalizedADB()
 		item.PasswordConfigured = passwordConfigured != 0
 		item.CreatedAt = parseTime(createdAt)
 		item.UpdatedAt = parseTime(updatedAt)
@@ -297,13 +304,14 @@ func (s *Store) ReplaceHardwareGroups(ctx context.Context, taskID string, groups
 			INSERT INTO hardware_resources(
 				id,description,mode,serial_device,serial_baudrate,network_host,network_port,
 				network_protocol,ssh_auth,username,credential_ref,password_configured,adb_paired_at,access,
-				created_at,updated_at
-			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
+				created_at,updated_at,adb_connection,adb_serial
+			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
 			resource.ID, resource.Description, resource.Mode, resource.Serial.Device,
 			resource.Serial.Baudrate, resource.Network.Host, resource.Network.Port,
 			resource.Network.Protocol, resource.Network.SSHAuth, resource.Username,
 			resource.CredentialRef, boolInt(resource.PasswordConfigured), resource.ADBPairedAt, resource.Access,
 			timeString(resource.CreatedAt), timeString(resource.UpdatedAt),
+			resource.Network.NormalizedADB().ADBConnection, resource.Network.NormalizedADB().ADBSerial,
 		)
 		if err != nil {
 			return fmt.Errorf("insert hardware resource %s: %w", resource.ID, err)
@@ -323,7 +331,8 @@ func (s *Store) ReplaceHardwareGroups(ctx context.Context, taskID string, groups
 						Access: group.Access, CreatedAt: group.CreatedAt, UpdatedAt: group.UpdatedAt,
 					})
 				}
-				if group.Network.Host != "" {
+				// USB direct ADB has a serial instead of a host.
+				if group.Network.Host != "" || group.Network.ADBUSB() && group.Network.ADBSerial != "" {
 					resources = append(resources, domain.HardwareResource{
 						ID: domain.NewID("hardware_resource"), Description: group.Description + " · 网络",
 						Mode: domain.HardwareModeNetwork, Network: group.Network, Username: group.Username,
diff --git a/internal/store/migrations.go b/internal/store/migrations.go
index 66dc7b9..729f8fe 100644
--- a/internal/store/migrations.go
+++ b/internal/store/migrations.go
@@ -4301,6 +4301,26 @@ func (s *Store) migrate(ctx context.Context) error {
 			return fmt.Errorf("record schema v155: %w", err)
 		}
 	}
+	// v156 splits ADB into wireless debugging and USB direct. Additive only:
+	// existing rows keep adb_connection='' which reads as wireless, so a phone
+	// that is already paired keeps its host, port and pairing state.
+	adbColumns := []struct{ name, ddl string }{
+		{"adb_connection", `ALTER TABLE hardware_resources ADD COLUMN adb_connection TEXT NOT NULL DEFAULT ''`},
+		{"adb_serial", `ALTER TABLE hardware_resources ADD COLUMN adb_serial TEXT NOT NULL DEFAULT ''`},
+	}
+	for _, column := range adbColumns {
+		if s.tableHasColumn(ctx, "hardware_resources", column.name) {
+			continue
+		}
+		if _, err := s.db.ExecContext(ctx, column.ddl); err != nil &&
+			!strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
+			return fmt.Errorf("apply schema v156 %s: %w", column.name, err)
+		}
+	}
+	if _, err := s.db.ExecContext(ctx,
+		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(156, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
+		return fmt.Errorf("record schema v156: %w", err)
+	}
 	return nil
 }
 
~~~~
