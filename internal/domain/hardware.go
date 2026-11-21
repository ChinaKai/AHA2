package domain

import (
	"net"
	"regexp"
	"strings"
	"time"
)

const (
	HardwareModeOff     = "off"
	HardwareModeSerial  = "serial"
	HardwareModeNetwork = "network"
	HardwareModeBoth    = "both"

	HardwareAccessReadOnly  = "read_only"
	HardwareAccessReadWrite = "read_write"

	HardwareTransportSerial  = "serial"
	HardwareTransportNetwork = "network"

	HardwareProtocolTelnet = "telnet"
	HardwareProtocolRaw    = "raw"
	HardwareProtocolSSH    = "ssh"
	HardwareProtocolADB    = "adb"

	HardwareSSHAuthAuto     = "auto"
	HardwareSSHAuthPassword = "password"
	HardwareSSHAuthKey      = "key"

	// ADB connection forms. All use network.protocol=adb; the form decides
	// which target fields apply. An empty value is the pre-split stored shape
	// and always means wireless, so existing paired phones keep their meaning.
	//   wireless: Android wireless debugging, host + connection port, optional
	//             pairing with a separate pairing port and six-digit code.
	//   tcp:      classic network ADB (adb tcpip), explicit host:port
	//             (default 5555), no pairing and no mDNS port discovery.
	//   usb:      device attached to the AHA host, addressed by serial only.
	HardwareADBConnectionWireless = "wireless"
	HardwareADBConnectionTCP      = "tcp"
	HardwareADBConnectionUSB      = "usb"
)

type HardwareSerialConfig struct {
	Device   string `json:"device"`
	Baudrate int    `json:"baudrate"`
}

type HardwareNetworkConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	SSHAuth  string `json:"ssh_auth"`
	// ADBConnection is wireless (pairing code + host:port), tcp (explicit
	// host:port, no pairing) or usb (device serial on the AHA host, no
	// host/port). Only meaningful for protocol=adb.
	ADBConnection string `json:"adb_connection,omitempty"`
	// ADBSerial is the USB device serial for ADBConnection=usb.
	ADBSerial string `json:"adb_serial,omitempty"`
}

// ADBUSB reports whether the config targets a USB-attached ADB device by serial.
func (config HardwareNetworkConfig) ADBUSB() bool {
	return config.Protocol == HardwareProtocolADB && config.ADBConnection == HardwareADBConnectionUSB
}

// ADBTCP reports whether the config is a classic network ADB target: the
// explicit host:port is connected as is, without pairing or mDNS discovery.
func (config HardwareNetworkConfig) ADBTCP() bool {
	return config.Protocol == HardwareProtocolADB && config.ADBConnection == HardwareADBConnectionTCP
}

// ADBWireless reports whether the config is an ADB wireless-debugging target
// (host:port, optional pairing). Legacy rows without a connection form count.
func (config HardwareNetworkConfig) ADBWireless() bool {
	return config.Protocol == HardwareProtocolADB &&
		config.ADBConnection != HardwareADBConnectionUSB && config.ADBConnection != HardwareADBConnectionTCP
}

// NormalizedADB fills the explicit connection form for ADB rows stored before
// the USB/wireless split and clears ADB-only fields for other protocols. It
// never changes host/port, so a legacy paired phone stays wireless.
func (config HardwareNetworkConfig) NormalizedADB() HardwareNetworkConfig {
	if config.Protocol != HardwareProtocolADB {
		config.ADBConnection = ""
		config.ADBSerial = ""
		return config
	}
	switch config.ADBConnection {
	case HardwareADBConnectionUSB:
	case HardwareADBConnectionTCP:
		config.ADBSerial = ""
	default:
		config.ADBConnection = HardwareADBConnectionWireless
		config.ADBSerial = ""
	}
	return config
}

// hasNetworkTarget reports whether the network side has a usable target:
// a USB serial for ADB USB, otherwise a host.
func (config HardwareNetworkConfig) hasNetworkTarget() bool {
	if config.ADBUSB() {
		return config.ADBSerial != ""
	}
	return config.Host != ""
}

var adbUSBSerialPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidADBUSBSerial accepts only serials that adb reports for USB devices.
// Network transports (host:port), mDNS service names and emulators are
// rejected so a USB resource can never be pointed at a network device, and
// adb -s device qualifiers (model:, usb:, product:) cannot be smuggled in.
// adb -s also resolves a bare host (192.0.2.4, localhost, tv.lan) to a
// connected host:5555 transport, so IP literals, dotted host names and
// localhost are rejected too. A dot never appears in a real USB serial we
// accept; the runtime get-serialno check is the second line of defence.
func ValidADBUSBSerial(serial string) bool {
	if !adbUSBSerialPattern.MatchString(serial) {
		return false
	}
	lower := strings.ToLower(serial)
	if strings.HasPrefix(lower, "emulator-") || strings.Contains(lower, "._adb") || strings.HasSuffix(lower, "._tcp") {
		return false
	}
	if strings.Contains(serial, ".") || lower == "localhost" || net.ParseIP(serial) != nil {
		return false
	}
	return true
}

type HardwareResource struct {
	ID                 string                `json:"id"`
	Description        string                `json:"description"`
	Mode               string                `json:"mode"`
	Serial             HardwareSerialConfig  `json:"serial"`
	Network            HardwareNetworkConfig `json:"network"`
	Username           string                `json:"username,omitempty"`
	CredentialRef      string                `json:"-"`
	PasswordConfigured bool                  `json:"password_configured"`
	ADBPairedAt        string                `json:"adb_paired_at,omitempty"`
	Access             string                `json:"access"`
	BindingCount       int                   `json:"binding_count"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
}

type TaskHardwareBinding struct {
	TaskID     string    `json:"task_id"`
	ID         string    `json:"id"`
	ResourceID string    `json:"resource_id"`
	Position   int       `json:"position"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (item HardwareResource) Supports(transport string) bool {
	switch transport {
	case HardwareTransportSerial:
		return (item.Mode == HardwareModeSerial || item.Mode == HardwareModeBoth) && item.Serial.Device != ""
	case HardwareTransportNetwork:
		return (item.Mode == HardwareModeNetwork || item.Mode == HardwareModeBoth) && item.Network.hasNetworkTarget()
	default:
		return false
	}
}

type HardwareGroup struct {
	TaskID             string                `json:"task_id"`
	ID                 string                `json:"id"`
	ResourceID         string                `json:"resource_id,omitempty"`
	Position           int                   `json:"position"`
	Description        string                `json:"description"`
	Mode               string                `json:"mode"`
	Serial             HardwareSerialConfig  `json:"serial"`
	Network            HardwareNetworkConfig `json:"network"`
	Username           string                `json:"username,omitempty"`
	CredentialRef      string                `json:"-"`
	PasswordConfigured bool                  `json:"password_configured"`
	Access             string                `json:"access"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
}

func (item HardwareGroup) Supports(transport string) bool {
	switch transport {
	case HardwareTransportSerial:
		return (item.Mode == HardwareModeSerial || item.Mode == HardwareModeBoth) && item.Serial.Device != ""
	case HardwareTransportNetwork:
		return (item.Mode == HardwareModeNetwork || item.Mode == HardwareModeBoth) && item.Network.hasNetworkTarget()
	default:
		return false
	}
}

func (item HardwareGroup) Writable() bool {
	return item.Access == HardwareAccessReadWrite
}

type HardwareIOEvent struct {
	Sequence   int64     `json:"sequence"`
	ID         string    `json:"id"`
	TaskID     string    `json:"task_id"`
	HardwareID string    `json:"hardware_id"`
	Transport  string    `json:"transport"`
	Direction  string    `json:"direction"`
	Data       string    `json:"data"`
	Encoding   string    `json:"encoding"`
	Source     string    `json:"source,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type HardwareIOPage struct {
	Items          []HardwareIOEvent `json:"items"`
	LatestSequence int64             `json:"latest_sequence"`
	HasMore        bool              `json:"has_more"`
}

type HardwareTerminalStatus struct {
	TaskID      string `json:"task_id"`
	HardwareID  string `json:"hardware_id"`
	Transport   string `json:"transport"`
	Endpoint    string `json:"endpoint"`
	Status      string `json:"status"`
	Connected   bool   `json:"connected"`
	ReadOnly    bool   `json:"read_only"`
	Error       string `json:"error,omitempty"`
	LoginStatus string `json:"login_status,omitempty"`
	LoginError  string `json:"login_error,omitempty"`
	// RecordError is set while terminal output cannot be stored (the live
	// stream still works); it clears once an event is stored again.
	RecordError string    `json:"record_error,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type SerialPort struct {
	Device      string `json:"device"`
	Description string `json:"description"`
	HardwareID  string `json:"hardware_id,omitempty"`
}
