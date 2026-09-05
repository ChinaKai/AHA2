package domain

import "time"

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

	HardwareSSHAuthAuto     = "auto"
	HardwareSSHAuthPassword = "password"
	HardwareSSHAuthKey      = "key"
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
}

type HardwareGroup struct {
	TaskID             string                `json:"task_id"`
	ID                 string                `json:"id"`
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
		return (item.Mode == HardwareModeNetwork || item.Mode == HardwareModeBoth) && item.Network.Host != ""
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
	TaskID      string    `json:"task_id"`
	HardwareID  string    `json:"hardware_id"`
	Transport   string    `json:"transport"`
	Endpoint    string    `json:"endpoint"`
	Status      string    `json:"status"`
	Connected   bool      `json:"connected"`
	ReadOnly    bool      `json:"read_only"`
	Error       string    `json:"error,omitempty"`
	LoginStatus string    `json:"login_status,omitempty"`
	LoginError  string    `json:"login_error,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type SerialPort struct {
	Device      string `json:"device"`
	Description string `json:"description"`
	HardwareID  string `json:"hardware_id,omitempty"`
}
