//go:build !windows

package hardware

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// fakeADB writes a shell-script adb whose body handles the arguments and logs
// every invocation (one line per call) so tests can assert which host-level
// commands a connection form did or did not run.
func fakeADB(t *testing.T, body string) (string, string) {
	t.Helper()
	directory := t.TempDir()
	executable := filepath.Join(directory, "adb")
	logPath := filepath.Join(directory, "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> \"" + logPath + "\"\n" + body + "\necho \"unexpected arguments: $*\" >&2\nexit 2\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return executable, logPath
}

func adbCalls(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func assertNoHostADBCommands(t *testing.T, calls []string, forbidden ...string) {
	t.Helper()
	for _, call := range calls {
		first := strings.Fields(call)
		if len(first) == 0 {
			continue
		}
		for _, command := range forbidden {
			if first[0] == command {
				t.Fatalf("unexpected host-level adb %s call: %q (all calls %q)", command, call, calls)
			}
		}
	}
}

func TestParseADBDevicesListsOnlyUSBAndBlocksAmbiguousSerials(t *testing.T) {
	t.Parallel()
	output := strings.Join([]string{
		"* daemon not running; starting now at tcp:5037",
		"* daemon started successfully",
		"List of devices attached",
		"R58M12345A             device usb:1-1 product:beyond1 model:SM_G973F device:beyond1 transport_id:1",
		"0123456789ABCDEF       unauthorized usb:1-2 transport_id:2",
		"NOPERM01               no permissions (user in plugdev group; are your udev rules wrong?); see [http://developer.android.com/tools/device.html] usb:1-3 transport_id:3",
		"192.168.1.5:5555       device product:tv model:BRAVIA device:tv transport_id:4",
		"adb-R58M-abc._adb-tls-connect._tcp device product:x model:y device:z transport_id:5",
		"emulator-5554          device product:sdk model:sdk device:emu transport_id:6",
		"DUP1                   device usb:1-4 transport_id:7",
		"DUP1                   device usb:1-5 transport_id:8",
		"WIN-NO-USB-ATTR        device product:p model:Pixel_7 device:panther transport_id:9",
		"",
	}, "\n")
	list := parseADBDevices(output)
	if list.OtherCount != 3 {
		t.Fatalf("network/mDNS/emulator transports must be counted but hidden: %#v", list)
	}
	byKey := map[string][]ADBUSBDevice{}
	for _, device := range list.Devices {
		byKey[device.Serial] = append(byKey[device.Serial], device)
	}
	if got := byKey["R58M12345A"]; len(got) != 1 || got[0].State != "device" || got[0].Model != "SM_G973F" || got[0].USB != "1-1" || !got[0].Selectable {
		t.Fatalf("USB device parse: %#v", got)
	}
	if got := byKey["0123456789ABCDEF"]; len(got) != 1 || got[0].State != "unauthorized" || !got[0].Selectable {
		t.Fatalf("unauthorized USB device parse: %#v", got)
	}
	if got := byKey["NOPERM01"]; len(got) != 1 || got[0].State != "no_permissions" || got[0].USB != "1-3" {
		t.Fatalf("no-permissions USB device parse: %#v", got)
	}
	if got := byKey["DUP1"]; len(got) != 2 || got[0].Selectable || got[1].Selectable {
		t.Fatalf("devices sharing a serial must not be selectable: %#v", got)
	}
	if got := byKey["WIN-NO-USB-ATTR"]; len(got) != 1 || !got[0].Selectable || got[0].Model != "Pixel_7" {
		t.Fatalf("USB device without usb: attribute (Windows adb) parse: %#v", got)
	}
	for _, hidden := range []string{"192.168.1.5:5555", "adb-R58M-abc._adb-tls-connect._tcp", "emulator-5554"} {
		if len(byKey[hidden]) != 0 {
			t.Fatalf("non-USB transport %q was listed", hidden)
		}
	}
	if empty := parseADBDevices("List of devices attached\n\n"); len(empty.Devices) != 0 || empty.Devices == nil {
		t.Fatalf("empty device list must be a non-nil empty slice: %#v", empty)
	}
}

func TestListADBUSBDevicesRunsOnlyDevicesList(t *testing.T) {
	t.Parallel()
	executable, logPath := fakeADB(t, `if [ "$1" = "devices" ] && [ "$2" = "-l" ]; then
  echo "List of devices attached"
  echo "SERIAL1 device usb:1-1 model:Phone transport_id:1"
  exit 0
fi`)
	list, err := listADBUSBDevicesWithExecutable(context.Background(), executable)
	if err != nil || len(list.Devices) != 1 || list.Devices[0].Serial != "SERIAL1" {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	if calls := adbCalls(t, logPath); len(calls) != 1 || calls[0] != "devices -l" {
		t.Fatalf("enumeration must run exactly adb devices -l: %q", calls)
	}
}

func TestExecuteADBUSBTargetsSerialWithoutNetworkCommands(t *testing.T) {
	// Not parallel: the authorization wait is a package variable other tests shrink.
	executable, logPath := fakeADB(t, `if [ "$1" = "-s" ] && [ "$2" = "R58M12345A" ] && [ "$3" = "get-state" ]; then
  echo device
  exit 0
fi
if [ "$1" = "-s" ] && [ "$2" = "R58M12345A" ] && [ "$3" = "exec-out" ] && [ "$4" = "screencap" ]; then
  printf '\211PNG\r\n'
  printf 'usb warning' >&2
  exit 3
fi`)
	result, err := executeADBUSBWithExecutable(context.Background(), executable, "R58M12345A",
		[]string{"exec-out", "screencap", "-p"}, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Endpoint != "R58M12345A" || result.ExitCode != 3 || string(result.Stdout) != "\x89PNG\r\n" || string(result.Stderr) != "usb warning" {
		t.Fatalf("unexpected USB result: %#v", result)
	}
	calls := adbCalls(t, logPath)
	assertNoHostADBCommands(t, calls, "connect", "pair", "mdns", "kill-server", "disconnect")
	for _, call := range calls {
		if !strings.HasPrefix(call, "-s R58M12345A ") {
			t.Fatalf("USB command not pinned to the bound serial: %q", call)
		}
	}
}

func TestExecuteADBUSBReportsDeviceStates(t *testing.T) {
	shrinkADBAuthorizeWait(t, 30*time.Millisecond)
	cases := []struct {
		name, body, want string
	}{
		{"missing", `if [ "$3" = "get-state" ]; then echo "error: device 'GONE1' not found" >&2; exit 1; fi`, "未在 AHA 宿主机上找到 USB 设备"},
		{"ambiguous", `if [ "$3" = "get-state" ]; then echo "error: more than one device/emulator" >&2; exit 1; fi`, "多台设备使用同一序列号"},
		{"offline", `if [ "$3" = "get-state" ]; then echo offline; exit 0; fi`, "离线"},
		{"unauthorized", `if [ "$3" = "get-state" ]; then echo "error: device unauthorized." >&2; exit 1; fi`, "未授权"},
		{"no_permissions", `if [ "$3" = "get-state" ]; then echo "error: insufficient permissions for device" >&2; exit 1; fi`, "没有访问 USB 设备"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			executable, logPath := fakeADB(t, testCase.body)
			_, err := executeADBUSBWithExecutable(context.Background(), executable, "GONE1", []string{"shell", "id"}, nil, 2*time.Second)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("err=%v want substring %q", err, testCase.want)
			}
			calls := adbCalls(t, logPath)
			assertNoHostADBCommands(t, calls, "connect", "pair", "mdns", "kill-server")
			for _, call := range calls {
				if strings.Contains(call, " shell id") {
					t.Fatalf("command ran on an unusable USB device: %q", calls)
				}
			}
		})
	}
}

func TestExecuteADBUSBWaitsForOwnerAuthorization(t *testing.T) {
	shrinkADBAuthorizeWait(t, 2*time.Second)
	directory := t.TempDir()
	marker := filepath.Join(directory, "count")
	executable, logPath := fakeADB(t, `if [ "$3" = "get-state" ]; then
  count=$(cat "`+marker+`" 2>/dev/null || echo 0)
  count=$((count+1))
  echo $count > "`+marker+`"
  if [ $count -lt 3 ]; then echo "error: device unauthorized." >&2; exit 1; fi
  echo device; exit 0
fi
if [ "$3" = "shell" ]; then echo ok; exit 0; fi`)
	result, err := executeADBUSBWithExecutable(context.Background(), executable, "SER1", []string{"shell", "true"}, nil, 5*time.Second)
	if err != nil || string(result.Stdout) != "ok\n" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertNoHostADBCommands(t, adbCalls(t, logPath), "kill-server", "connect", "pair")
}

func TestExecuteADBUSBRejectsNonUSBSerialsBeforeRunningADB(t *testing.T) {
	executable, logPath := fakeADB(t, ``)
	t.Setenv(adbExecutableEnvironment, executable)
	for _, serial := range []string{"", "192.0.2.4:5555", "adb-x._adb-tls-connect._tcp", "emulator-5554", "-s", "model:Pixel", "a b", "usb:1-1", "x;rm"} {
		if _, err := ExecuteADBUSB(context.Background(), serial, []string{"shell", "id"}, nil, time.Second); err == nil {
			t.Fatalf("serial %q was accepted", serial)
		}
		if _, err := openADBUSBTerminal(context.Background(), serial); err == nil {
			t.Fatalf("terminal serial %q was accepted", serial)
		}
	}
	if calls := adbCalls(t, logPath); len(calls) != 0 {
		t.Fatalf("invalid serials reached adb: %q", calls)
	}
}

func TestOpenADBUSBTerminalStartsShellOnSerial(t *testing.T) {
	// Not parallel: the authorization wait is a package variable other tests shrink.
	executable, logPath := fakeADB(t, `if [ "$3" = "get-state" ]; then echo device; exit 0; fi
if [ "$1" = "-s" ] && [ "$2" = "SER1" ] && [ "$3" = "shell" ] && [ "$4" = "-t" ] && [ "$5" = "-t" ]; then
  echo "usb shell"
  IFS= read -r line
  exit 0
fi`)
	terminal, err := openADBUSBTerminalWithExecutable(context.Background(), executable, "SER1")
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if resolved, ok := terminal.(*adbTerminal); !ok || resolved.ResolvedEndpoint() != "USB SER1" {
		t.Fatalf("USB terminal endpoint: %#v", terminal)
	}
	buffer := make([]byte, 64)
	count, _ := terminal.Read(buffer)
	if !strings.Contains(string(buffer[:count]), "usb shell") {
		t.Fatalf("USB shell output %q", buffer[:count])
	}
	assertNoHostADBCommands(t, adbCalls(t, logPath), "connect", "pair", "mdns", "kill-server")
}

// Network direct (classic adb tcpip): the explicit host:port is connected as
// is even when mDNS would advertise a different port for the same host.
func TestExecuteADBTCPUsesExplicitEndpointWithoutMDNSOrPairing(t *testing.T) {
	// Not parallel: the authorization wait is a package variable other tests shrink.
	executable, logPath := fakeADB(t, `if [ "$1" = "mdns" ]; then
  echo "adb-test _adb-tls-connect._tcp 192.0.2.44:45678"
  exit 0
fi
if [ "$1" = "connect" ] && [ "$2" = "192.0.2.44:5555" ]; then echo "connected to $2"; exit 0; fi
if [ "$1" = "-s" ] && [ "$2" = "192.0.2.44:5555" ] && [ "$3" = "get-state" ]; then echo device; exit 0; fi
if [ "$1" = "-s" ] && [ "$2" = "192.0.2.44:5555" ] && [ "$3" = "shell" ]; then echo tcp-ok; exit 0; fi`)
	result, err := executeADBTCPWithExecutable(context.Background(), executable, "192.0.2.44:5555", []string{"shell", "id"}, nil, 5*time.Second)
	if err != nil || result.Endpoint != "192.0.2.44:5555" || string(result.Stdout) != "tcp-ok\n" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	calls := adbCalls(t, logPath)
	assertNoHostADBCommands(t, calls, "mdns", "pair", "kill-server")
	if len(calls) == 0 || calls[0] != "connect 192.0.2.44:5555" {
		t.Fatalf("network direct must connect the explicit endpoint first: %q", calls)
	}
}

func TestExecuteADBTCPCustomPortAndUnauthorizedDoesNotRestartServer(t *testing.T) {
	shrinkADBAuthorizeWait(t, 30*time.Millisecond)
	executable, logPath := fakeADB(t, `if [ "$1" = "connect" ] && [ "$2" = "192.0.2.50:5601" ]; then echo "connected to $2"; exit 0; fi
if [ "$1" = "-s" ] && [ "$2" = "192.0.2.50:5601" ] && [ "$3" = "get-state" ]; then echo "error: device unauthorized." >&2; exit 1; fi`)
	_, err := executeADBTCPWithExecutable(context.Background(), executable, "192.0.2.50:5601", []string{"shell", "id"}, nil, 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "未授权") {
		t.Fatalf("err=%v", err)
	}
	assertNoHostADBCommands(t, adbCalls(t, logPath), "kill-server", "mdns", "pair")
}

func TestOpenADBTCPTerminalKeepsExplicitEndpoint(t *testing.T) {
	// Not parallel: the authorization wait is a package variable other tests shrink.
	executable, logPath := fakeADB(t, `if [ "$1" = "mdns" ]; then echo "adb-test _adb-tls-connect._tcp 192.0.2.44:45678"; exit 0; fi
if [ "$1" = "connect" ] && [ "$2" = "192.0.2.44:5555" ]; then echo "connected to $2"; exit 0; fi
if [ "$3" = "get-state" ]; then echo device; exit 0; fi
if [ "$1" = "-s" ] && [ "$2" = "192.0.2.44:5555" ] && [ "$3" = "shell" ]; then echo "tcp shell"; IFS= read -r line; exit 0; fi`)
	terminal, err := openADBTCPTerminalWithExecutable(context.Background(), executable, "192.0.2.44:5555")
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if resolved, ok := terminal.(*adbTerminal); !ok || resolved.ResolvedEndpoint() != "192.0.2.44:5555" {
		t.Fatalf("TCP terminal endpoint: %#v", terminal)
	}
	assertNoHostADBCommands(t, adbCalls(t, logPath), "mdns", "pair", "kill-server")
}

func TestManagerRoutesEachADBConnectionFormToItsOpener(t *testing.T) {
	t.Parallel()
	type call struct{ opener, target string }
	calls := make(chan call, 8)
	manager := NewManager(&memoryRecorder{})
	defer manager.Close()
	pipe := func() io.ReadWriteCloser {
		client, server := net.Pipe()
		t.Cleanup(func() { server.Close() })
		return client
	}
	manager.openADB = func(_ context.Context, endpoint string) (io.ReadWriteCloser, error) {
		calls <- call{"wireless", endpoint}
		return pipe(), nil
	}
	manager.openADBTCP = func(_ context.Context, endpoint string) (io.ReadWriteCloser, error) {
		calls <- call{"tcp", endpoint}
		return pipe(), nil
	}
	manager.openADBUSB = func(_ context.Context, serial string) (io.ReadWriteCloser, error) {
		calls <- call{"usb", serial}
		return pipe(), nil
	}
	groups := []struct {
		group          domain.HardwareGroup
		opener, target string
		endpoint       string
	}{
		{domain.HardwareGroup{ID: "legacy", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "192.0.2.10", Port: 41234, Protocol: domain.HardwareProtocolADB}}, "wireless", "192.0.2.10:41234", "192.0.2.10:41234"},
		{domain.HardwareGroup{ID: "tcp", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "192.0.2.11", Port: 5555, Protocol: domain.HardwareProtocolADB, ADBConnection: domain.HardwareADBConnectionTCP}}, "tcp", "192.0.2.11:5555", "192.0.2.11:5555"},
		{domain.HardwareGroup{ID: "usb", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Protocol: domain.HardwareProtocolADB, ADBConnection: domain.HardwareADBConnectionUSB, ADBSerial: "SER9"}}, "usb", "SER9", "USB SER9"},
	}
	for _, item := range groups {
		item.group.Access = domain.HardwareAccessReadWrite
		status, err := manager.Connect(context.Background(), ConnectRequest{TaskID: "task-1", HardwareID: item.group.ID, Transport: "network", Group: item.group})
		if err != nil || !status.Connected {
			t.Fatalf("%s connect: %#v %v", item.group.ID, status, err)
		}
		got := <-calls
		if got.opener != item.opener || got.target != item.target {
			t.Fatalf("%s routed to %#v, want %s %s", item.group.ID, got, item.opener, item.target)
		}
		if status.Endpoint != item.endpoint {
			t.Fatalf("%s status endpoint %q want %q", item.group.ID, status.Endpoint, item.endpoint)
		}
	}
	// A USB group without a valid serial must not connect anywhere.
	bad := domain.HardwareGroup{ID: "bad", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadWrite,
		Network: domain.HardwareNetworkConfig{Protocol: domain.HardwareProtocolADB, ADBConnection: domain.HardwareADBConnectionUSB, ADBSerial: "192.0.2.4:5555"}}
	if _, err := manager.Connect(context.Background(), ConnectRequest{TaskID: "task-1", HardwareID: "bad", Transport: "network", Group: bad}); err == nil {
		t.Fatal("USB group with a network-style serial connected")
	}
	select {
	case extra := <-calls:
		t.Fatalf("invalid USB group reached an opener: %#v", extra)
	default:
	}
}
