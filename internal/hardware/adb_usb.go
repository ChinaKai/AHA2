package hardware

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// ADB USB direct: the device is attached to the machine running AHA and is
// addressed only by its serial. Nothing here runs adb pair, adb connect or
// mDNS discovery, and nothing restarts the shared adb server, so a USB target
// can never be redirected to a network device or disturb other devices.

// ADBUSBDevice is one USB-attached device reported by `adb devices -l` on the
// AHA host. Fields are external data and are sanitized before leaving here.
type ADBUSBDevice struct {
	Serial     string `json:"serial"`
	State      string `json:"state"`
	Model      string `json:"model,omitempty"`
	Product    string `json:"product,omitempty"`
	Device     string `json:"device,omitempty"`
	USB        string `json:"usb,omitempty"`
	Selectable bool   `json:"selectable"`
}

// ADBUSBDeviceList is the enumeration result. OtherCount counts network,
// mDNS and emulator transports that were deliberately left out.
type ADBUSBDeviceList struct {
	Devices    []ADBUSBDevice `json:"devices"`
	OtherCount int            `json:"other_count"`
}

// USBDisplayEndpoint is how a USB target is shown in terminal status.
func USBDisplayEndpoint(serial string) string {
	return "USB " + serial
}

// ListADBUSBDevices enumerates USB devices on the AHA host. It is an Owner
// resource-configuration capability; Task Agents never reach it.
func ListADBUSBDevices(ctx context.Context) (ADBUSBDeviceList, error) {
	executable, err := resolveADBExecutable()
	if err != nil {
		return ADBUSBDeviceList{}, err
	}
	return listADBUSBDevicesWithExecutable(ctx, executable)
}

func listADBUSBDevicesWithExecutable(ctx context.Context, executable string) (ADBUSBDeviceList, error) {
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(listCtx, executable, "devices", "-l")
	configureADBCommand(command)
	output, err := command.CombinedOutput()
	if err != nil {
		return ADBUSBDeviceList{}, fmt.Errorf("adb devices: %s", adbFailure(sanitizeADBText(string(output), 512), err))
	}
	return parseADBDevices(string(output)), nil
}

// parseADBDevices reads `adb devices -l`. Network transports (host:port),
// mDNS service names and emulators are counted but not listed, so the Owner
// can only pick a USB device for a USB resource.
func parseADBDevices(output string) ADBUSBDeviceList {
	result := ADBUSBDeviceList{Devices: []ADBUSBDevice{}}
	seen := map[string]int{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(strings.ToLower(line), "list of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		serial := fields[0]
		rest := fields[1:]
		state := rest[0]
		rest = rest[1:]
		if state == "no" && len(rest) > 0 && rest[0] == "permissions" {
			state = "no_permissions"
			rest = rest[1:]
			// Skip the parenthesised hint adb prints after "no permissions".
			if len(rest) > 0 && strings.HasPrefix(rest[0], "(") {
				for len(rest) > 0 {
					word := rest[0]
					rest = rest[1:]
					if strings.HasSuffix(word, ")") || strings.HasSuffix(word, ");") {
						break
					}
				}
			}
		}
		attributes := map[string]string{}
		for _, field := range rest {
			key, value, ok := strings.Cut(field, ":")
			if ok {
				attributes[key] = value
			}
		}
		if adbNetworkSerial(serial) {
			result.OtherCount++
			continue
		}
		device := ADBUSBDevice{
			Serial:     sanitizeADBText(serial, 128),
			State:      sanitizeADBText(state, 32),
			Model:      sanitizeADBText(attributes["model"], 128),
			Product:    sanitizeADBText(attributes["product"], 128),
			Device:     sanitizeADBText(attributes["device"], 128),
			USB:        sanitizeADBText(attributes["usb"], 64),
			Selectable: domain.ValidADBUSBSerial(serial),
		}
		if index, duplicate := seen[serial]; duplicate {
			// Two devices sharing one serial cannot be told apart by adb -s;
			// neither may be selected.
			result.Devices[index].Selectable = false
			device.Selectable = false
		} else {
			seen[serial] = len(result.Devices)
		}
		result.Devices = append(result.Devices, device)
	}
	return result
}

func adbNetworkSerial(serial string) bool {
	lower := strings.ToLower(serial)
	return strings.Contains(serial, ":") || strings.HasPrefix(lower, "emulator-") ||
		strings.Contains(lower, "._adb") || strings.HasSuffix(lower, "._tcp")
}

func sanitizeADBText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

// ExecuteADBUSB runs one device command on the USB device with this serial.
func ExecuteADBUSB(ctx context.Context, serial string, arguments []string, input []byte, timeout time.Duration) (ADBExecResult, error) {
	if len(arguments) == 0 {
		return ADBExecResult{}, fmt.Errorf("ADB command arguments are required")
	}
	if !domain.ValidADBUSBSerial(serial) {
		return ADBExecResult{}, fmt.Errorf("USB 设备序列号无效")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > maxADBExecTimeout {
		timeout = maxADBExecTimeout
	}
	executable, err := resolveADBExecutable()
	if err != nil {
		return ADBExecResult{}, err
	}
	return executeADBUSBWithExecutable(ctx, executable, serial, arguments, input, timeout)
}

func executeADBUSBWithExecutable(ctx context.Context, executable, serial string, arguments []string, input []byte, timeout time.Duration) (ADBExecResult, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	if err := ensureADBUSBDevice(commandCtx, executable, serial); err != nil {
		return ADBExecResult{}, err
	}
	return runADBDeviceCommand(commandCtx, executable, serial, arguments, input, timeout, startedAt)
}

func openADBUSBTerminal(ctx context.Context, serial string) (io.ReadWriteCloser, error) {
	if !domain.ValidADBUSBSerial(serial) {
		return nil, fmt.Errorf("USB 设备序列号无效")
	}
	executable, err := resolveADBExecutable()
	if err != nil {
		return nil, err
	}
	return openADBUSBTerminalWithExecutable(ctx, executable, serial)
}

func openADBUSBTerminalWithExecutable(ctx context.Context, executable, serial string) (io.ReadWriteCloser, error) {
	checkCtx, cancel := context.WithTimeout(ctx, adbAuthorizeWait+15*time.Second)
	defer cancel()
	if err := ensureADBUSBDevice(checkCtx, executable, serial); err != nil {
		return nil, err
	}
	return startADBShell(executable, serial, USBDisplayEndpoint(serial))
}

// ensureADBUSBDevice confirms the serial is attached and authorized. An
// unauthorized device gets one window for the Owner to accept the prompt on
// the device; nothing here accepts or bypasses that prompt.
func ensureADBUSBDevice(ctx context.Context, executable, serial string) error {
	deadline := time.Now().Add(adbAuthorizeWait)
	for {
		state, detail := adbUSBDeviceState(ctx, executable, serial)
		switch state {
		case "device":
			return nil
		case "missing":
			return fmt.Errorf("未在 AHA 宿主机上找到 USB 设备 %s：请确认数据线已连接、设备已开启 USB 调试，并在硬件资源页刷新设备列表", serial)
		case "ambiguous":
			return fmt.Errorf("AHA 宿主机上有多台设备使用同一序列号 %s，无法确定目标：请只保留其中一台后重试", serial)
		case "offline":
			return fmt.Errorf("USB 设备 %s 处于离线状态：请重新插拔数据线，或在设备上关闭再打开 USB 调试后重试", serial)
		case "no_permissions":
			return fmt.Errorf("AHA 宿主机没有访问 USB 设备 %s 的权限：请检查宿主机 USB 驱动或权限设置", serial)
		case "unauthorized":
			if time.Now().After(deadline) {
				return fmt.Errorf("USB 设备 %s 未授权：请在设备上确认「允许 USB 调试」弹窗（建议勾选一律允许）后重试", serial)
			}
		case "error":
			return fmt.Errorf("读取 USB 设备 %s 状态失败：%s", serial, detail)
		default:
			return fmt.Errorf("USB 设备 %s 状态异常：%s", serial, state)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(adbAuthorizePollInterval):
		}
	}
}

// adbUSBDeviceState maps `adb -s <serial> get-state` to device, unauthorized,
// offline, no_permissions, missing, ambiguous, error or another raw state.
func adbUSBDeviceState(ctx context.Context, executable, serial string) (string, string) {
	stateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(stateCtx, executable, "-s", serial, "get-state")
	configureADBCommand(command)
	output, err := command.CombinedOutput()
	message := strings.ToLower(strings.TrimSpace(string(output)))
	if err == nil {
		state := sanitizeADBText(message, 32)
		if state == "" {
			return "error", "adb 未返回设备状态"
		}
		return state, ""
	}
	switch {
	case strings.Contains(message, "more than one"):
		return "ambiguous", ""
	case strings.Contains(message, "not found"), strings.Contains(message, "no devices"):
		return "missing", ""
	case strings.Contains(message, "unauthorized"):
		return "unauthorized", ""
	case strings.Contains(message, "no permissions"), strings.Contains(message, "insufficient permissions"):
		return "no_permissions", ""
	case strings.Contains(message, "offline"):
		return "offline", ""
	}
	if ctx.Err() != nil {
		return "error", ctx.Err().Error()
	}
	return "error", adbFailure(sanitizeADBText(string(output), 512), err)
}
