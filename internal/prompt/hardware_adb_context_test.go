package prompt

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestHardwareContextShowsADBConnectionForms(t *testing.T) {
	t.Parallel()
	groups := hardwareContextGroups([]domain.HardwareGroup{
		{ID: "legacy", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadWrite,
			Network: domain.HardwareNetworkConfig{Host: "192.0.2.10", Port: 41234, Protocol: domain.HardwareProtocolADB}},
		{ID: "tv", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadWrite,
			Network: domain.HardwareNetworkConfig{Host: "192.0.2.11", Port: 5555, Protocol: domain.HardwareProtocolADB, ADBConnection: domain.HardwareADBConnectionTCP}},
		{ID: "board", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadOnly,
			Network: domain.HardwareNetworkConfig{Protocol: domain.HardwareProtocolADB, ADBConnection: domain.HardwareADBConnectionUSB, ADBSerial: "SERIAL-A"}},
	})
	for _, name := range []string{"templates/context-hardware.md", "templates/zh-CN/context-hardware.md"} {
		content, err := templateFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		rendered, err := renderTemplate(name, string(content), templateData{HardwareGroups: groups})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, want := range []string{"192.0.2.10:41234", "192.0.2.11:5555", "SERIAL-A"} {
			if !strings.Contains(rendered, want) {
				t.Fatalf("%s missing %q:\n%s", name, want, rendered)
			}
		}
		// A USB group has no host:port; the template must not print an empty one.
		if strings.Contains(rendered, ":0") || strings.Contains(rendered, " :") {
			t.Fatalf("%s printed an empty network endpoint for USB:\n%s", name, rendered)
		}
	}
}
