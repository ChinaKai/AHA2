package hardwareconfig

import (
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func normalizeNetwork(t *testing.T, network domain.HardwareNetworkConfig) (domain.HardwareGroup, error) {
	t.Helper()
	return NormalizeGroup("", "res", 0, GroupPayload{Mode: domain.HardwareModeNetwork, Network: network}, domain.HardwareGroup{}, time.Unix(1, 0))
}

func TestNormalizeGroupADBConnectionForms(t *testing.T) {
	t.Parallel()
	// Legacy shape: protocol=adb without a connection form stays wireless with
	// its host and port; it is never reinterpreted as network direct or USB.
	legacy, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Host: "192.0.2.5", Port: 41235, Protocol: "adb", ADBSerial: "STRAY"})
	if err != nil || legacy.Network.ADBConnection != domain.HardwareADBConnectionWireless ||
		legacy.Network.Host != "192.0.2.5" || legacy.Network.Port != 41235 || legacy.Network.ADBSerial != "" {
		t.Fatalf("legacy adb: %#v %v", legacy.Network, err)
	}
	wirelessDefault, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Host: "192.0.2.5", Protocol: "adb", ADBConnection: "wireless"})
	if err != nil || wirelessDefault.Network.Port != 5555 {
		t.Fatalf("wireless default port: %#v %v", wirelessDefault.Network, err)
	}
	tcp, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Host: "192.0.2.6", Protocol: "adb", ADBConnection: "tcp", ADBSerial: "STRAY"})
	if err != nil || !tcp.Network.ADBTCP() || tcp.Network.Port != 5555 || tcp.Network.Host != "192.0.2.6" || tcp.Network.ADBSerial != "" {
		t.Fatalf("tcp default 5555: %#v %v", tcp.Network, err)
	}
	tcpCustom, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Host: "192.0.2.6", Port: 5601, Protocol: "adb", ADBConnection: "tcp"})
	if err != nil || tcpCustom.Network.Port != 5601 {
		t.Fatalf("tcp custom port: %#v %v", tcpCustom.Network, err)
	}
	if _, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Protocol: "adb", ADBConnection: "tcp"}); err == nil {
		t.Fatal("tcp without host was accepted")
	}
	// USB direct drops host/port that a client may still send and needs a serial.
	usb, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Host: "192.0.2.7", Port: 5555, Protocol: "adb", ADBConnection: "usb", ADBSerial: " R58M12345A "})
	if err != nil || !usb.Network.ADBUSB() || usb.Network.Host != "" || usb.Network.Port != 0 || usb.Network.ADBSerial != "R58M12345A" {
		t.Fatalf("usb: %#v %v", usb.Network, err)
	}
	if !usb.Supports(domain.HardwareTransportNetwork) {
		t.Fatal("USB group with a serial must support the network transport")
	}
	if _, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Protocol: "adb", ADBConnection: "usb"}); err == nil || !strings.Contains(err.Error(), "USB 设备序列号") {
		t.Fatalf("usb without serial: %v", err)
	}
	for _, serial := range []string{"192.0.2.4:5555", "adb-R58M._adb-tls-connect._tcp", "emulator-5554", "-s", "model:Pixel", "usb:1-1", "a b", "x;rm -rf", strings.Repeat("a", 129), "192.0.2.4", "localhost", "LocalHost", "tv.lan", "10.0.0.1"} {
		if _, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Protocol: "adb", ADBConnection: "usb", ADBSerial: serial}); err == nil {
			t.Fatalf("invalid USB serial %q was accepted", serial)
		}
	}
	if _, err := normalizeNetwork(t, domain.HardwareNetworkConfig{Host: "h", Protocol: "adb", ADBConnection: "bluetooth"}); err == nil {
		t.Fatal("unknown ADB connection form was accepted")
	}
	// Other protocols never carry ADB fields.
	ssh, err := NormalizeGroup("", "res", 0, GroupPayload{Mode: domain.HardwareModeNetwork, Username: "root",
		Network: domain.HardwareNetworkConfig{Host: "h", Protocol: "ssh", ADBConnection: "usb", ADBSerial: "SER"}}, domain.HardwareGroup{}, time.Unix(1, 0))
	if err != nil || ssh.Network.ADBConnection != "" || ssh.Network.ADBSerial != "" {
		t.Fatalf("ssh kept ADB fields: %#v %v", ssh.Network, err)
	}
}

func TestADBPairingStillValid(t *testing.T) {
	t.Parallel()
	wireless := domain.HardwareNetworkConfig{Host: "192.0.2.5", Port: 41235, Protocol: "adb", ADBConnection: "wireless"}
	legacy := domain.HardwareNetworkConfig{Host: "192.0.2.5", Port: 5555, Protocol: "adb"}
	network := domain.HardwareModeNetwork
	if !ADBPairingStillValid(network, legacy, network, wireless) {
		t.Fatal("pairing must survive an edit that keeps the same wireless host (legacy row)")
	}
	tcp := wireless
	tcp.ADBConnection = "tcp"
	usb := domain.HardwareNetworkConfig{Protocol: "adb", ADBConnection: "usb", ADBSerial: "SER"}
	otherHost := wireless
	otherHost.Host = "192.0.2.9"
	ssh := wireless
	ssh.Protocol = "ssh"
	for name, after := range map[string]domain.HardwareNetworkConfig{"tcp": tcp, "usb": usb, "other host": otherHost, "ssh": ssh} {
		if ADBPairingStillValid(network, wireless, network, after) {
			t.Fatalf("pairing survived a switch to %s", name)
		}
	}
	if ADBPairingStillValid(network, usb, network, wireless) || ADBPairingStillValid(network, tcp, network, wireless) {
		t.Fatal("a non-wireless resource has no pairing to carry into wireless")
	}
	if ADBPairingStillValid(network, wireless, domain.HardwareModeSerial, wireless) {
		t.Fatal("pairing survived a switch to serial mode")
	}
}
