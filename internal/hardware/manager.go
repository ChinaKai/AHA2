package hardware

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var (
	ErrNotConnected = errors.New("hardware terminal is not connected")
	ErrReadOnly     = errors.New("hardware terminal is read-only")
)

type Recorder interface {
	AppendHardwareIO(context.Context, domain.HardwareIOEvent) (int64, error)
}

type ConnectRequest struct {
	TaskID     string
	HardwareID string
	Transport  string
	Group      domain.HardwareGroup
	Password   string
	ReadOnly   bool
}

type LiveEvent struct {
	Sequence int64
	Type     string
	Data     []byte
	Status   domain.HardwareTerminalStatus
}

type attachment struct {
	taskID     string
	hardwareID string
	readOnly   bool
}

type liveSubscriber struct {
	attachment attachment
	events     chan LiveEvent
}

type terminalSession struct {
	manager   *Manager
	targetKey string
	endpoint  string
	transport string
	protocol  string
	sshAuth   string
	username  string
	password  string
	baudrate  int
	conn      io.ReadWriteCloser
	codec     *telnetCodec
	decoder   streamDecoder
	startedAt time.Time

	mu             sync.RWMutex
	writeMu        sync.Mutex
	attachments    map[string]attachment
	subscribers    map[string]liveSubscriber
	status         string
	lastError      string
	updatedAt      time.Time
	closing        bool
	usernameSent   bool
	passwordSent   bool
	automaticWrite bool
}

type Manager struct {
	recorder Recorder
	now      func() time.Time

	openSerial  func(string, int) (io.ReadWriteCloser, error)
	dialNetwork func(context.Context, string, string) (net.Conn, error)
	openSSH     func(context.Context, string, string, string, string) (io.ReadWriteCloser, error)

	mu          sync.Mutex
	sessions    map[string]*terminalSession
	attachments map[string]*terminalSession
}

func NewManager(recorder Recorder) *Manager {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &Manager{
		recorder:   recorder,
		now:        time.Now,
		openSerial: openSerial,
		dialNetwork: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
		openSSH:     openSSHTerminal,
		sessions:    map[string]*terminalSession{},
		attachments: map[string]*terminalSession{},
	}
}

func (manager *Manager) SerialPorts() []domain.SerialPort {
	result := listSerialPorts()
	if result == nil {
		return []domain.SerialPort{}
	}
	return result
}

func (manager *Manager) Connect(ctx context.Context, request ConnectRequest) (domain.HardwareTerminalStatus, error) {
	if request.TaskID == "" || request.HardwareID == "" {
		return domain.HardwareTerminalStatus{}, fmt.Errorf("task and hardware ids are required")
	}
	if !request.Group.Supports(request.Transport) {
		return domain.HardwareTerminalStatus{}, fmt.Errorf("hardware group does not support %s", request.Transport)
	}
	targetKey, endpoint, protocol, baudrate, err := sessionTarget(request.Group, request.Transport)
	if err != nil {
		return domain.HardwareTerminalStatus{}, err
	}
	attachmentKey := attachmentKey(request.TaskID, request.HardwareID, request.Transport)
	sshAuth := normalizeSSHAuth(request.Group.Network.SSHAuth)

	manager.mu.Lock()
	if current := manager.attachments[attachmentKey]; current != nil {
		if current.targetKey == targetKey && current.isConnected() {
			status := current.statusFor(request.TaskID, request.HardwareID, request.ReadOnly)
			manager.mu.Unlock()
			return status, nil
		}
		manager.detachLocked(attachmentKey, current)
	}
	if current := manager.sessions[targetKey]; current != nil && current.isConnected() {
		if current.username != request.Group.Username || current.password != request.Password ||
			current.sshAuth != sshAuth || current.baudrate != baudrate {
			manager.mu.Unlock()
			return domain.HardwareTerminalStatus{}, fmt.Errorf("%s is already connected with different settings", endpoint)
		}
		current.mu.Lock()
		current.attachments[attachmentKey] = attachment{
			taskID: request.TaskID, hardwareID: request.HardwareID, readOnly: request.ReadOnly,
		}
		current.mu.Unlock()
		manager.attachments[attachmentKey] = current
		status := current.statusFor(request.TaskID, request.HardwareID, request.ReadOnly)
		manager.mu.Unlock()
		current.record("system", "attached to shared hardware terminal", "runtime", nil, nil)
		return status, nil
	}

	var connection io.ReadWriteCloser
	if request.Transport == domain.HardwareTransportSerial {
		connection, err = manager.openSerial(request.Group.Serial.Device, baudrate)
	} else if protocol == domain.HardwareProtocolSSH {
		connection, err = manager.openSSH(ctx, endpoint, request.Group.Username, request.Password, sshAuth)
	} else {
		connection, err = manager.dialNetwork(ctx, "tcp", endpoint)
	}
	if err != nil {
		manager.mu.Unlock()
		return domain.HardwareTerminalStatus{}, fmt.Errorf("connect %s: %w", endpoint, err)
	}
	now := manager.now().UTC()
	session := &terminalSession{
		manager: manager, targetKey: targetKey, endpoint: endpoint, transport: request.Transport,
		protocol: protocol, sshAuth: sshAuth, username: request.Group.Username, password: request.Password, baudrate: baudrate,
		conn: connection, startedAt: now, updatedAt: now, status: "running",
		attachments: map[string]attachment{
			attachmentKey: {taskID: request.TaskID, hardwareID: request.HardwareID, readOnly: request.ReadOnly},
		},
		subscribers: map[string]liveSubscriber{},
	}
	if protocol == domain.HardwareProtocolTelnet {
		session.codec = newTelnetCodec(100, 28)
	}
	manager.sessions[targetKey] = session
	manager.attachments[attachmentKey] = session
	manager.mu.Unlock()

	session.record("system", "hardware terminal connected", "runtime", nil, nil)
	if session.codec != nil {
		_ = session.writeRaw(session.codec.InitialNegotiation())
	}
	go session.readLoop()
	return session.statusFor(request.TaskID, request.HardwareID, request.ReadOnly), nil
}

func (manager *Manager) Status(taskID, hardwareID, transport string, readOnly bool) domain.HardwareTerminalStatus {
	key := attachmentKey(taskID, hardwareID, transport)
	manager.mu.Lock()
	session := manager.attachments[key]
	manager.mu.Unlock()
	if session == nil {
		return domain.HardwareTerminalStatus{
			TaskID: taskID, HardwareID: hardwareID, Transport: transport,
			Status: "disconnected", ReadOnly: readOnly,
		}
	}
	return session.statusFor(taskID, hardwareID, readOnly)
}

func (manager *Manager) Send(taskID, hardwareID, transport, data, encoding, source string) error {
	key := attachmentKey(taskID, hardwareID, transport)
	manager.mu.Lock()
	session := manager.attachments[key]
	manager.mu.Unlock()
	if session == nil {
		return ErrNotConnected
	}
	session.mu.RLock()
	item, ok := session.attachments[key]
	session.mu.RUnlock()
	if !ok {
		return ErrNotConnected
	}
	if item.readOnly {
		return ErrReadOnly
	}
	if encoding == "" {
		encoding = "text"
	}
	var payload []byte
	switch encoding {
	case "text":
		payload = []byte(data)
	case "hex":
		if transport != domain.HardwareTransportSerial {
			return fmt.Errorf("hex input is supported for serial only")
		}
		var err error
		payload, err = decodeHex(data)
		if err != nil {
			return err
		}
	case "base64":
		var err error
		payload, err = base64.StdEncoding.DecodeString(data)
		if err != nil {
			return fmt.Errorf("invalid base64 input: %w", err)
		}
	default:
		return fmt.Errorf("unsupported encoding %q", encoding)
	}
	if len(payload) == 0 {
		return fmt.Errorf("hardware input is empty")
	}
	if len(payload) > 64*1024 {
		return fmt.Errorf("hardware input exceeds 64 KiB")
	}
	display := data
	if encoding == "hex" {
		display = strings.ToUpper(hex.EncodeToString(payload))
	} else if encoding == "base64" {
		display = strings.ToValidUTF8(string(payload), "\uFFFD")
	}
	return session.sendPayload(payload, display, source)
}

func (manager *Manager) SendRaw(taskID, hardwareID, transport string, payload []byte, source string) error {
	if len(payload) == 0 {
		return nil
	}
	if len(payload) > 64*1024 {
		return fmt.Errorf("hardware input exceeds 64 KiB")
	}
	key := attachmentKey(taskID, hardwareID, transport)
	manager.mu.Lock()
	session := manager.attachments[key]
	manager.mu.Unlock()
	if session == nil {
		return ErrNotConnected
	}
	session.mu.RLock()
	item, ok := session.attachments[key]
	session.mu.RUnlock()
	if !ok {
		return ErrNotConnected
	}
	if item.readOnly {
		return ErrReadOnly
	}
	display := strings.ToValidUTF8(string(payload), "\uFFFD")
	return session.sendPayload(payload, display, source)
}

func (manager *Manager) Subscribe(
	taskID, hardwareID, transport string,
) (<-chan LiveEvent, func(), error) {
	key := attachmentKey(taskID, hardwareID, transport)
	manager.mu.Lock()
	session := manager.attachments[key]
	manager.mu.Unlock()
	if session == nil {
		return nil, nil, ErrNotConnected
	}
	session.mu.Lock()
	item, ok := session.attachments[key]
	if !ok || session.closing {
		session.mu.Unlock()
		return nil, nil, ErrNotConnected
	}
	id := domain.NewID("hardware_subscriber")
	events := make(chan LiveEvent, 256)
	session.subscribers[id] = liveSubscriber{attachment: item, events: events}
	session.mu.Unlock()
	cancel := func() {
		session.mu.Lock()
		if _, exists := session.subscribers[id]; exists {
			delete(session.subscribers, id)
		}
		session.mu.Unlock()
	}
	return events, cancel, nil
}

func (manager *Manager) Resize(taskID, hardwareID, transport string, cols, rows int) error {
	key := attachmentKey(taskID, hardwareID, transport)
	manager.mu.Lock()
	session := manager.attachments[key]
	manager.mu.Unlock()
	if session == nil {
		return ErrNotConnected
	}
	if cols < 20 {
		cols = 20
	}
	if cols > 320 {
		cols = 320
	}
	if rows < 8 {
		rows = 8
	}
	if rows > 120 {
		rows = 120
	}
	if session.codec != nil {
		if payload := session.codec.Resize(cols, rows); len(payload) > 0 {
			return session.writeRaw(payload)
		}
	}
	if value, ok := session.conn.(interface{ Resize(int, int) error }); ok {
		return value.Resize(cols, rows)
	}
	return nil
}

func (manager *Manager) Disconnect(taskID, hardwareID, transport string) {
	key := attachmentKey(taskID, hardwareID, transport)
	manager.mu.Lock()
	session := manager.attachments[key]
	if session != nil {
		manager.detachLocked(key, session)
	}
	manager.mu.Unlock()
}

func (manager *Manager) DisconnectTask(taskID string) {
	manager.mu.Lock()
	for key, session := range manager.attachments {
		if strings.HasPrefix(key, taskID+"\x00") {
			manager.detachLocked(key, session)
		}
	}
	manager.mu.Unlock()
}

func (manager *Manager) Close() error {
	manager.mu.Lock()
	sessions := make([]*terminalSession, 0, len(manager.sessions))
	for _, session := range manager.sessions {
		sessions = append(sessions, session)
	}
	manager.sessions = map[string]*terminalSession{}
	manager.attachments = map[string]*terminalSession{}
	manager.mu.Unlock()
	for _, session := range sessions {
		session.close("service shutdown")
	}
	return nil
}

func (manager *Manager) detachLocked(key string, session *terminalSession) {
	delete(manager.attachments, key)
	session.mu.Lock()
	delete(session.attachments, key)
	remaining := len(session.attachments)
	session.mu.Unlock()
	if remaining == 0 {
		delete(manager.sessions, session.targetKey)
		session.close("hardware terminal disconnected")
	}
}

func (session *terminalSession) isConnected() bool {
	session.mu.RLock()
	defer session.mu.RUnlock()
	return !session.closing && session.status == "running"
}

func (session *terminalSession) statusFor(taskID, hardwareID string, readOnly bool) domain.HardwareTerminalStatus {
	session.mu.RLock()
	defer session.mu.RUnlock()
	return domain.HardwareTerminalStatus{
		TaskID: taskID, HardwareID: hardwareID, Transport: session.transport,
		Endpoint: session.endpoint, Status: session.status, Connected: !session.closing && session.status == "running",
		ReadOnly: readOnly, Error: session.lastError, StartedAt: session.startedAt, UpdatedAt: session.updatedAt,
	}
}

func (session *terminalSession) readLoop() {
	buffer := make([]byte, 4096)
	for {
		count, err := session.conn.Read(buffer)
		if count > 0 {
			payload := append([]byte(nil), buffer[:count]...)
			if session.codec != nil {
				var reply []byte
				payload, reply = session.codec.Feed(payload)
				if len(reply) > 0 {
					_ = session.writeRaw(reply)
				}
			}
			if text, terminalData := session.decoder.Decode(payload); len(terminalData) > 0 {
				session.record("rx", text, "runtime", terminalData, nil)
				session.maybeAutoLogin(text)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) || session.isClosing() {
				break
			}
			session.fail(err)
			break
		}
	}
}

func (session *terminalSession) maybeAutoLogin(text string) {
	lower := strings.ToLower(text)
	session.mu.Lock()
	username := session.username
	password := session.password
	sendUsername := username != "" && !session.usernameSent && (strings.Contains(lower, "login:") || strings.Contains(lower, "username:"))
	sendPassword := password != "" && !session.passwordSent && strings.Contains(lower, "password:")
	if sendUsername {
		session.usernameSent = true
	}
	if sendPassword {
		session.passwordSent = true
	}
	session.mu.Unlock()
	if sendUsername {
		session.automaticSend(username+"\r\n", username+"\\r\\n")
	}
	if sendPassword {
		session.automaticSend(password+"\r\n", "<password>\\r\\n")
	}
}

func (session *terminalSession) automaticSend(value, display string) {
	payload := []byte(value)
	if session.codec != nil {
		payload = session.codec.Encode(payload)
	}
	if session.writeRaw(payload) == nil {
		session.record("tx", display, "auto-login", nil, nil)
	}
}

func (session *terminalSession) sendPayload(payload []byte, display, source string) error {
	wire := payload
	if session.codec != nil {
		wire = session.codec.Encode(payload)
	}
	if err := session.writeRaw(wire); err != nil {
		return err
	}
	session.record("tx", display, source, nil, nil)
	return nil
}

func (session *terminalSession) writeRaw(payload []byte) error {
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	for len(payload) > 0 {
		count, err := session.conn.Write(payload)
		if err != nil {
			session.fail(err)
			return err
		}
		if count <= 0 {
			err = io.ErrShortWrite
			session.fail(err)
			return err
		}
		payload = payload[count:]
	}
	return nil
}

func (session *terminalSession) record(direction, data, source string, raw []byte, only *attachment) {
	if direction == "rx" {
		if data == "" && len(raw) == 0 {
			return
		}
	} else if strings.TrimSpace(data) == "" {
		return
	}
	session.mu.RLock()
	targets := make([]attachment, 0, len(session.attachments))
	if only != nil {
		targets = append(targets, *only)
	} else {
		for _, item := range session.attachments {
			targets = append(targets, item)
		}
	}
	session.mu.RUnlock()
	now := session.manager.now().UTC()
	for _, target := range targets {
		var sequence int64
		if session.manager.recorder == nil {
			if direction == "rx" && len(raw) > 0 {
				session.broadcastOutput(target, sequence, raw)
			}
			continue
		}
		sequence, _ = session.manager.recorder.AppendHardwareIO(context.Background(), domain.HardwareIOEvent{
			ID: domain.NewID("hardware_io"), TaskID: target.taskID, HardwareID: target.hardwareID,
			Transport: session.transport, Direction: direction, Data: data, Encoding: "text",
			Source: source, CreatedAt: now,
		})
		if direction == "rx" && len(raw) > 0 {
			session.broadcastOutput(target, sequence, raw)
		}
	}
	session.mu.Lock()
	session.updatedAt = now
	session.mu.Unlock()
}

func (session *terminalSession) fail(err error) {
	session.mu.Lock()
	if session.closing {
		session.mu.Unlock()
		return
	}
	session.status = "error"
	session.lastError = err.Error()
	session.updatedAt = session.manager.now().UTC()
	session.mu.Unlock()
	session.record("system", "hardware terminal error: "+err.Error(), "runtime", nil, nil)
	session.broadcastStatus()
	_ = session.conn.Close()
}

func (session *terminalSession) close(reason string) {
	session.mu.Lock()
	if session.closing {
		session.mu.Unlock()
		return
	}
	session.closing = true
	session.status = "disconnected"
	session.updatedAt = session.manager.now().UTC()
	session.mu.Unlock()
	session.record("system", reason, "runtime", nil, nil)
	session.broadcastStatus()
	_ = session.conn.Close()
}

func (session *terminalSession) broadcastOutput(target attachment, sequence int64, data []byte) {
	session.mu.RLock()
	subscribers := make([]chan LiveEvent, 0, len(session.subscribers))
	for _, subscriber := range session.subscribers {
		if subscriber.attachment.taskID == target.taskID && subscriber.attachment.hardwareID == target.hardwareID {
			subscribers = append(subscribers, subscriber.events)
		}
	}
	session.mu.RUnlock()
	event := LiveEvent{Sequence: sequence, Type: "output", Data: append([]byte(nil), data...)}
	for _, events := range subscribers {
		select {
		case events <- event:
		default:
		}
	}
}

func (session *terminalSession) broadcastStatus() {
	session.mu.RLock()
	subscribers := make([]liveSubscriber, 0, len(session.subscribers))
	for _, subscriber := range session.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	session.mu.RUnlock()
	for _, subscriber := range subscribers {
		status := session.statusFor(
			subscriber.attachment.taskID,
			subscriber.attachment.hardwareID,
			subscriber.attachment.readOnly,
		)
		select {
		case subscriber.events <- LiveEvent{Type: "status", Status: status}:
		default:
		}
	}
}

func (session *terminalSession) isClosing() bool {
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.closing
}

func attachmentKey(taskID, hardwareID, transport string) string {
	return taskID + "\x00" + hardwareID + "\x00" + transport
}

func sessionTarget(group domain.HardwareGroup, transport string) (key, endpoint, protocol string, baudrate int, err error) {
	switch transport {
	case domain.HardwareTransportSerial:
		endpoint = strings.TrimSpace(group.Serial.Device)
		baudrate = group.Serial.Baudrate
		if baudrate <= 0 {
			baudrate = 115200
		}
		if endpoint == "" {
			err = fmt.Errorf("serial device is required")
			return
		}
		key = "serial:" + strings.ToLower(endpoint)
	case domain.HardwareTransportNetwork:
		host := strings.TrimSpace(group.Network.Host)
		port := group.Network.Port
		if port <= 0 {
			port = 23
		}
		protocol = strings.TrimSpace(group.Network.Protocol)
		if protocol == "" {
			protocol = domain.HardwareProtocolTelnet
		}
		if protocol != domain.HardwareProtocolTelnet && protocol != domain.HardwareProtocolRaw && protocol != domain.HardwareProtocolSSH {
			err = fmt.Errorf("unsupported network protocol %q", protocol)
			return
		}
		if host == "" {
			err = fmt.Errorf("network host is required")
			return
		}
		endpoint = net.JoinHostPort(host, strconv.Itoa(port))
		key = "network:" + protocol + ":" + strings.ToLower(endpoint)
	default:
		err = fmt.Errorf("unsupported hardware transport %q", transport)
	}
	return
}

func decodeHex(value string) ([]byte, error) {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "", "0x", "", "0X", "", ",", "")
	value = replacer.Replace(value)
	if len(value)%2 != 0 {
		return nil, fmt.Errorf("hex input must contain complete bytes")
	}
	data, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid hex input: %w", err)
	}
	return data, nil
}

func normalizeSSHAuth(value string) string {
	switch strings.TrimSpace(value) {
	case domain.HardwareSSHAuthPassword:
		return domain.HardwareSSHAuthPassword
	case domain.HardwareSSHAuthKey:
		return domain.HardwareSSHAuthKey
	default:
		return domain.HardwareSSHAuthAuto
	}
}

type streamDecoder struct {
	pending []byte
}

func (decoder *streamDecoder) Decode(chunk []byte) (string, []byte) {
	combined := append(append([]byte(nil), decoder.pending...), chunk...)
	data := combined
	decoder.pending = nil
	var output strings.Builder
	for len(data) > 0 {
		if !utf8.FullRune(data) {
			decoder.pending = append([]byte(nil), data...)
			break
		}
		runeValue, size := utf8.DecodeRune(data)
		if runeValue == utf8.RuneError && size == 1 {
			output.WriteRune(utf8.RuneError)
			data = data[1:]
			continue
		}
		output.Write(data[:size])
		data = data[size:]
	}
	consumed := len(combined) - len(decoder.pending)
	return output.String(), append([]byte(nil), combined[:consumed]...)
}
