package hardware

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type memoryRecorder struct {
	mu     sync.Mutex
	events []domain.HardwareIOEvent
}

type resizableConnection struct {
	net.Conn
	resize chan [2]int
}

func TestDecodeHexAcceptsCommonByteFormats(t *testing.T) {
	t.Parallel()
	data, err := decodeHex("A0 01, 0x05\nA6")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string([]byte{0xA0, 0x01, 0x05, 0xA6}) {
		t.Fatalf("decoded bytes = % X", data)
	}
}

func TestBase64FallbackInput(t *testing.T) {
	t.Parallel()
	device, bridge := net.Pipe()
	defer device.Close()
	defer bridge.Close()
	manager := NewManager(nil)
	key := attachmentKey("task-1", "board", "serial")
	session := &terminalSession{
		manager: manager, conn: device, transport: "serial",
		attachments: map[string]attachment{
			key: {taskID: "task-1", hardwareID: "board"},
		},
		subscribers: map[string]liveSubscriber{},
	}
	manager.attachments[key] = session
	received := make(chan string, 1)
	go func() {
		buffer := make([]byte, 16)
		count, _ := bridge.Read(buffer)
		received <- string(buffer[:count])
	}()
	if err := manager.Send("task-1", "board", "serial", "G1tB", "base64", "web"); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-received:
		if value != "\x1b[A" {
			t.Fatalf("fallback payload = %q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("fallback input timed out")
	}
}

func (connection *resizableConnection) Resize(cols, rows int) error {
	connection.resize <- [2]int{cols, rows}
	return nil
}

func (recorder *memoryRecorder) AppendHardwareIO(_ context.Context, item domain.HardwareIOEvent) (int64, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	item.Sequence = int64(len(recorder.events) + 1)
	recorder.events = append(recorder.events, item)
	return item.Sequence, nil
}

func (recorder *memoryRecorder) contains(direction, data string) bool {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for _, item := range recorder.events {
		if item.Direction == direction && item.Data == data {
			return true
		}
	}
	return false
}

func TestManagerSerialReadWriteAndPermission(t *testing.T) {
	t.Parallel()
	recorder := &memoryRecorder{}
	manager := NewManager(recorder)
	manager.now = func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	device, bridge := net.Pipe()
	manager.openSerial = func(string, int) (io.ReadWriteCloser, error) { return device, nil }
	defer manager.Close()
	defer bridge.Close()

	group := domain.HardwareGroup{
		ID: "board", Mode: domain.HardwareModeSerial,
		Serial: domain.HardwareSerialConfig{Device: "COM3", Baudrate: 115200},
		Access: domain.HardwareAccessReadWrite,
	}
	status, err := manager.Connect(context.Background(), ConnectRequest{
		TaskID: "task-1", HardwareID: group.ID, Transport: "serial", Group: group,
	})
	if err != nil || !status.Connected {
		t.Fatalf("connect: %#v %v", status, err)
	}
	go bridge.Write([]byte("boot ok\r\n"))
	waitForHardwareEvent(t, recorder, "rx", "boot ok\r\n")
	read := make(chan string, 1)
	go func() {
		buffer := make([]byte, 32)
		count, _ := bridge.Read(buffer)
		read <- string(buffer[:count])
	}()
	if err := manager.Send("task-1", "board", "serial", "help\r\n", "text", "web"); err != nil {
		t.Fatal(err)
	}
	if value := <-read; value != "help\r\n" {
		t.Fatalf("unexpected serial write %q", value)
	}
	manager.Disconnect("task-1", "board", "serial")
	if manager.Status("task-1", "board", "serial", false).Connected {
		t.Fatal("serial remained connected")
	}

	device2, bridge2 := net.Pipe()
	manager.openSerial = func(string, int) (io.ReadWriteCloser, error) { return device2, nil }
	defer bridge2.Close()
	if _, err := manager.Connect(context.Background(), ConnectRequest{
		TaskID: "task-1", HardwareID: group.ID, Transport: "serial", Group: group, ReadOnly: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Send("task-1", "board", "serial", "x", "text", "web"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("expected read-only error, got %v", err)
	}
}

func TestManagerNetworkTelnetAndAutoLogin(t *testing.T) {
	t.Parallel()
	recorder := &memoryRecorder{}
	manager := NewManager(recorder)
	client, server := net.Pipe()
	manager.dialNetwork = func(context.Context, string, string) (net.Conn, error) { return client, nil }
	defer manager.Close()
	defer server.Close()
	group := domain.HardwareGroup{
		ID: "board", Mode: domain.HardwareModeNetwork,
		Network:  domain.HardwareNetworkConfig{Host: "127.0.0.1", Port: 23, Protocol: domain.HardwareProtocolTelnet},
		Username: "root", Access: domain.HardwareAccessReadWrite,
	}
	go func() {
		buffer := make([]byte, 128)
		_, _ = server.Read(buffer)
		_, _ = server.Write([]byte("login:"))
		_, _ = server.Read(buffer)
		_, _ = server.Write([]byte("password:"))
		_, _ = server.Read(buffer)
	}()
	if _, err := manager.Connect(context.Background(), ConnectRequest{
		TaskID: "task-1", HardwareID: group.ID, Transport: "network", Group: group, Password: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	waitForHardwareEvent(t, recorder, "tx", "root\\r\\n")
	waitForHardwareEvent(t, recorder, "tx", "<password>\\r\\n")
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for _, item := range recorder.events {
		if item.Data == "secret\r\n" || item.Data == "secret" {
			t.Fatal("password leaked into hardware history")
		}
	}
}

func TestManagerNetworkSSHUsesInteractiveTerminal(t *testing.T) {
	t.Parallel()
	recorder := &memoryRecorder{}
	manager := NewManager(recorder)
	client, server := net.Pipe()
	manager.openSSH = func(_ context.Context, endpoint, username, password, authMode string) (io.ReadWriteCloser, error) {
		if endpoint != "127.0.0.1:22" || username != "root" || password != "secret" || authMode != domain.HardwareSSHAuthPassword {
			t.Fatalf("unexpected SSH options: %s %s %s %s", endpoint, username, password, authMode)
		}
		return client, nil
	}
	defer manager.Close()
	defer server.Close()
	group := domain.HardwareGroup{
		ID: "board", Mode: domain.HardwareModeNetwork,
		Network: domain.HardwareNetworkConfig{
			Host: "127.0.0.1", Port: 22, Protocol: domain.HardwareProtocolSSH, SSHAuth: domain.HardwareSSHAuthPassword,
		},
		Username: "root", Access: domain.HardwareAccessReadWrite,
	}
	if _, err := manager.Connect(context.Background(), ConnectRequest{
		TaskID: "task-1", HardwareID: group.ID, Transport: "network", Group: group, Password: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	go server.Write([]byte("shell ready\r\n"))
	waitForHardwareEvent(t, recorder, "rx", "shell ready\r\n")
}

func TestManagerLiveSubscriptionRawInputAndResize(t *testing.T) {
	t.Parallel()
	recorder := &memoryRecorder{}
	manager := NewManager(recorder)
	client, server := net.Pipe()
	resizable := &resizableConnection{Conn: client, resize: make(chan [2]int, 1)}
	manager.openSSH = func(_ context.Context, _, _, _, _ string) (io.ReadWriteCloser, error) {
		return resizable, nil
	}
	defer manager.Close()
	defer server.Close()
	group := domain.HardwareGroup{
		ID: "board", Mode: domain.HardwareModeNetwork,
		Network: domain.HardwareNetworkConfig{
			Host: "127.0.0.1", Port: 22, Protocol: domain.HardwareProtocolSSH, SSHAuth: domain.HardwareSSHAuthPassword,
		},
		Username: "root", Access: domain.HardwareAccessReadWrite,
	}
	if _, err := manager.Connect(context.Background(), ConnectRequest{
		TaskID: "task-1", HardwareID: group.ID, Transport: "network", Group: group, Password: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	events, cancel, err := manager.Subscribe("task-1", group.ID, "network")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	go server.Write([]byte("\x1b[32mready\x1b[0m\r\n"))
	select {
	case event := <-events:
		if event.Type != "output" || string(event.Data) != "\x1b[32mready\x1b[0m\r\n" || event.Sequence == 0 {
			t.Fatalf("unexpected live event: %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("live output timed out")
	}
	read := make(chan string, 1)
	go func() {
		buffer := make([]byte, 32)
		count, _ := server.Read(buffer)
		read <- string(buffer[:count])
	}()
	if err := manager.SendRaw("task-1", group.ID, "network", []byte("\x03"), "websocket"); err != nil {
		t.Fatal(err)
	}
	if value := <-read; value != "\x03" {
		t.Fatalf("unexpected raw input %q", value)
	}
	if err := manager.Resize("task-1", group.ID, "network", 120, 40); err != nil {
		t.Fatal(err)
	}
	if size := <-resizable.resize; size != [2]int{120, 40} {
		t.Fatalf("unexpected resize %v", size)
	}
}

func TestTelnetCodecStripsNegotiation(t *testing.T) {
	t.Parallel()
	codec := newTelnetCodec(100, 28)
	output, reply := codec.Feed([]byte{'o', 'k', telnetIAC, telnetWILL, telnetEcho, '\r', '\n'})
	if string(output) != "ok\r\n" {
		t.Fatalf("unexpected output %q", output)
	}
	if len(reply) != 3 || reply[1] != telnetDO || reply[2] != telnetEcho {
		t.Fatalf("unexpected reply %v", reply)
	}
}

func TestStreamDecoderPreservesSplitUTF8Bytes(t *testing.T) {
	t.Parallel()
	var decoder streamDecoder
	firstText, firstBytes := decoder.Decode([]byte{0xe4, 0xb8})
	if firstText != "" || len(firstBytes) != 0 {
		t.Fatalf("partial rune emitted: %q %x", firstText, firstBytes)
	}
	secondText, secondBytes := decoder.Decode([]byte{0xad, '\r', '\n'})
	if secondText != "中\r\n" || string(secondBytes) != "中\r\n" {
		t.Fatalf("split rune lost: %q %x", secondText, secondBytes)
	}
}

func waitForHardwareEvent(t *testing.T, recorder *memoryRecorder, direction, data string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if recorder.contains(direction, data) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("event not recorded: %s %q", direction, data)
}
