// Package hardwareconfig owns the shared validation and normalization rules for
// task hardware groups. HTTP handlers and the owner-delegate pet both accept the
// same payload shape, so the rules live here instead of in either transport.
package hardwareconfig

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var invalidID = regexp.MustCompile(`[^a-z0-9_-]+`)

type GroupPayload struct {
	ID                    string                       `json:"id"`
	Description           string                       `json:"description"`
	Mode                  string                       `json:"mode"`
	Serial                domain.HardwareSerialConfig  `json:"serial"`
	Network               domain.HardwareNetworkConfig `json:"network"`
	Username              string                       `json:"username"`
	Password              string                       `json:"password"`
	ReuseRemoteCredential bool                         `json:"reuse_remote_credential"`
	ClearSecret           bool                         `json:"clear_password"`
	Access                string                       `json:"access"`
}

func NormalizeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = invalidID.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-_")
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

func NormalizeGroup(
	taskID, id string,
	position int,
	raw GroupPayload,
	existing domain.HardwareGroup,
	now time.Time,
) (domain.HardwareGroup, error) {
	mode := strings.TrimSpace(raw.Mode)
	if mode == "" {
		mode = domain.HardwareModeOff
	}
	switch mode {
	case domain.HardwareModeOff, domain.HardwareModeSerial, domain.HardwareModeNetwork, domain.HardwareModeBoth:
	default:
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的模式无效", id)
	}
	access := strings.TrimSpace(raw.Access)
	if access == "" {
		access = domain.HardwareAccessReadWrite
	}
	if access != domain.HardwareAccessReadOnly && access != domain.HardwareAccessReadWrite {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的权限无效", id)
	}
	description := strings.TrimSpace(raw.Description)
	if utf8.RuneCountInString(description) > 2000 {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的描述超过 2000 字符", id)
	}
	raw.Serial.Device = strings.TrimSpace(raw.Serial.Device)
	if raw.Serial.Baudrate <= 0 {
		raw.Serial.Baudrate = 115200
	}
	raw.Network.Host = strings.TrimSpace(raw.Network.Host)
	raw.Network.Protocol = strings.TrimSpace(raw.Network.Protocol)
	if raw.Network.Protocol == "" {
		raw.Network.Protocol = domain.HardwareProtocolTelnet
	}
	if raw.Network.Protocol != domain.HardwareProtocolTelnet &&
		raw.Network.Protocol != domain.HardwareProtocolRaw &&
		raw.Network.Protocol != domain.HardwareProtocolSSH &&
		raw.Network.Protocol != domain.HardwareProtocolADB {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的网络协议无效", id)
	}
	raw.Network.SSHAuth = strings.TrimSpace(raw.Network.SSHAuth)
	if raw.Network.SSHAuth == "" {
		raw.Network.SSHAuth = domain.HardwareSSHAuthAuto
	}
	if raw.Network.SSHAuth != domain.HardwareSSHAuthAuto &&
		raw.Network.SSHAuth != domain.HardwareSSHAuthPassword &&
		raw.Network.SSHAuth != domain.HardwareSSHAuthKey {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的 SSH 登录方式无效", id)
	}
	if raw.Network.Port <= 0 {
		switch raw.Network.Protocol {
		case domain.HardwareProtocolSSH:
			raw.Network.Port = 22
		case domain.HardwareProtocolADB:
			raw.Network.Port = 5555
		default:
			raw.Network.Port = 23
		}
	}
	if raw.Network.Port > 65535 {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的网络端口无效", id)
	}
	if mode == domain.HardwareModeSerial && raw.Serial.Device == "" {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 缺少串口设备", id)
	}
	if mode == domain.HardwareModeNetwork && raw.Network.Host == "" {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 缺少网络地址", id)
	}
	if (mode == domain.HardwareModeNetwork || mode == domain.HardwareModeBoth) &&
		raw.Network.Protocol == domain.HardwareProtocolSSH &&
		strings.TrimSpace(raw.Username) == "" {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的 SSH 用户名不能为空", id)
	}
	if mode == domain.HardwareModeBoth && raw.Serial.Device == "" && raw.Network.Host == "" {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 至少需要一种连接", id)
	}
	createdAt := existing.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	username := strings.TrimSpace(raw.Username)
	if raw.Network.Protocol == domain.HardwareProtocolADB {
		username = ""
	}
	return domain.HardwareGroup{
		TaskID: taskID, ID: id, Position: position, Description: description, Mode: mode,
		Serial: raw.Serial, Network: raw.Network, Username: username,
		CredentialRef: existing.CredentialRef, PasswordConfigured: existing.PasswordConfigured,
		Access: access, CreatedAt: createdAt, UpdatedAt: now,
	}, nil
}
