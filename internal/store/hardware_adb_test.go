package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestHardwareResourceADBConnectionFormsPersistAndLegacyStaysWireless(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// A row written before v156 (no connection form) that was already paired.
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO hardware_resources(id,description,mode,serial_device,serial_baudrate,network_host,network_port,
			network_protocol,ssh_auth,username,credential_ref,password_configured,adb_paired_at,access,created_at,updated_at)
		VALUES('legacy','Phone','network','',115200,'192.0.2.5',41235,'adb','auto','','',0,'2026-11-01T00:00:00Z','read_write',
			'2026-11-01T00:00:00Z','2026-11-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, resource := range []domain.HardwareResource{
		{ID: "tcp", Description: "TV", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadWrite,
			Network: domain.HardwareNetworkConfig{Host: "192.0.2.6", Port: 5555, Protocol: "adb", SSHAuth: "auto", ADBConnection: "tcp"}},
		{ID: "usb", Description: "Board", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadOnly,
			Network: domain.HardwareNetworkConfig{Protocol: "adb", SSHAuth: "auto", ADBConnection: "usb", ADBSerial: "R58M12345A"}},
		// A stray serial on a non-ADB resource is never stored.
		{ID: "ssh", Description: "Router", Mode: domain.HardwareModeNetwork, Access: domain.HardwareAccessReadWrite,
			Network: domain.HardwareNetworkConfig{Host: "192.0.2.1", Port: 22, Protocol: "ssh", SSHAuth: "auto", ADBConnection: "usb", ADBSerial: "X"}},
	} {
		if err := database.UpsertHardwareResource(ctx, resource); err != nil {
			t.Fatal(err)
		}
	}
	database.Close()

	// Reopen: v156 must be reentrant and must not rewrite existing rows.
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	check := func(resource domain.HardwareResource) {
		t.Helper()
		switch resource.ID {
		case "legacy":
			if !resource.Network.ADBWireless() || resource.Network.ADBConnection != "wireless" ||
				resource.Network.Host != "192.0.2.5" || resource.Network.Port != 41235 || resource.ADBPairedAt == "" {
				t.Fatalf("legacy ADB row changed meaning: %#v", resource)
			}
		case "tcp":
			if !resource.Network.ADBTCP() || resource.Network.Host != "192.0.2.6" || resource.Network.Port != 5555 || resource.Network.ADBSerial != "" {
				t.Fatalf("tcp row: %#v", resource.Network)
			}
		case "usb":
			if !resource.Network.ADBUSB() || resource.Network.ADBSerial != "R58M12345A" || resource.Network.Host != "" {
				t.Fatalf("usb row: %#v", resource.Network)
			}
			if !resource.Supports(domain.HardwareTransportNetwork) {
				t.Fatal("USB resource must support the network transport")
			}
		case "ssh":
			if resource.Network.ADBConnection != "" || resource.Network.ADBSerial != "" {
				t.Fatalf("ssh row kept ADB fields: %#v", resource.Network)
			}
		}
	}
	list, err := database.HardwareResources(ctx)
	if err != nil || len(list) != 4 {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	for _, resource := range list {
		check(resource)
		single, err := database.HardwareResource(ctx, resource.ID)
		if err != nil {
			t.Fatal(err)
		}
		check(single)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var storedConnection string
	if err := raw.QueryRowContext(ctx, `SELECT adb_connection FROM hardware_resources WHERE id='legacy'`).Scan(&storedConnection); err != nil {
		t.Fatal(err)
	}
	if storedConnection != "" {
		t.Fatalf("migration rewrote the legacy row's stored connection form to %q", storedConnection)
	}
	var recorded int
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=156`).Scan(&recorded); err != nil || recorded != 1 {
		t.Fatalf("schema v156 recorded=%d err=%v", recorded, err)
	}
}

// The legacy Task-local "both" shape splits into a serial and a network
// resource; a USB direct ADB side must survive the split.
func TestReplaceHardwareGroupsKeepsUSBNetworkSideOfBothMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	if err := database.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{{
		ID: "board", Mode: domain.HardwareModeBoth, Access: domain.HardwareAccessReadWrite,
		Serial:  domain.HardwareSerialConfig{Device: "/dev/ttyUSB0", Baudrate: 115200},
		Network: domain.HardwareNetworkConfig{Protocol: "adb", SSHAuth: "auto", ADBConnection: "usb", ADBSerial: "SERIAL-A"},
	}}); err != nil {
		t.Fatal(err)
	}
	groups, err := database.HardwareGroups(ctx, task.ID)
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups=%#v err=%v", groups, err)
	}
	var usb domain.HardwareGroup
	for _, group := range groups {
		if group.Mode == domain.HardwareModeNetwork {
			usb = group
		}
	}
	if !usb.Network.ADBUSB() || usb.Network.ADBSerial != "SERIAL-A" || !usb.Supports(domain.HardwareTransportNetwork) {
		t.Fatalf("USB side of a both-mode group was lost: %#v", groups)
	}
}
