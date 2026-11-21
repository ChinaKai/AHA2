package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetHardwareResourcesADBConnectionForms(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	usb := decodePetHardwareView(t, petHardwareToolCall(t, fixture, "aha_create_hardware_resource",
		`{"id":"hw-usb","description":"USB 板","mode":"network","network":{"protocol":"adb","adb_connection":"usb","adb_serial":"R58M12345A","host":"192.0.2.3","port":5555}}`))
	if !usb.Network.ADBUSB() || usb.Network.ADBSerial != "R58M12345A" || usb.Network.Host != "" || usb.Network.Port != 0 {
		t.Fatalf("pet USB create: %#v", usb.Network)
	}
	tcp := decodePetHardwareView(t, petHardwareToolCall(t, fixture, "aha_create_hardware_resource",
		`{"id":"hw-tcp","description":"电视","mode":"network","network":{"protocol":"adb","adb_connection":"tcp","host":"192.0.2.4"}}`))
	if !tcp.Network.ADBTCP() || tcp.Network.Port != 5555 || tcp.Network.Host != "192.0.2.4" {
		t.Fatalf("pet TCP create: %#v", tcp.Network)
	}
	legacy := decodePetHardwareView(t, petHardwareToolCall(t, fixture, "aha_create_hardware_resource",
		`{"id":"hw-wireless","description":"手机","mode":"network","network":{"protocol":"adb","host":"192.0.2.5","port":41235}}`))
	if !legacy.Network.ADBWireless() || legacy.Network.ADBConnection != domain.HardwareADBConnectionWireless {
		t.Fatalf("pet create without adb_connection must be wireless: %#v", legacy.Network)
	}

	for _, args := range []string{
		`{"id":"hw-bad1","mode":"network","network":{"protocol":"adb","adb_connection":"usb"}}`,
		`{"id":"hw-bad2","mode":"network","network":{"protocol":"adb","adb_connection":"usb","adb_serial":"192.0.2.9:5555"}}`,
		`{"id":"hw-bad3","mode":"network","network":{"protocol":"adb","adb_connection":"tcp"}}`,
		`{"id":"hw-bad4","mode":"network","network":{"protocol":"adb","adb_connection":"bluetooth","host":"h"}}`,
		`{"id":"hw-bad5","mode":"network","network":{"protocol":"adb","adb_connection":"usb","adb_serial":"S1","pairing_code":"123456"}}`,
	} {
		_, err := fixture.service.PetMCPTool(fixture.ctx, "aha_create_hardware_resource", json.RawMessage(args))
		if err == nil {
			t.Fatalf("invalid ADB resource was accepted: %s", args)
		}
		if strings.Contains(args, "pairing_code") && !strings.Contains(err.Error(), "hardware_credential_owner_only") {
			t.Fatalf("pairing code must be refused as Owner-only: %v", err)
		}
	}

	// Partial update: only adb_connection changes. Switching wireless→usb
	// needs a serial; switching to usb with a serial drops host/port and the
	// recorded pairing.
	stored, err := fixture.database.HardwareResource(fixture.ctx, "hw-wireless")
	if err != nil {
		t.Fatal(err)
	}
	stored.ADBPairedAt = "2026-11-20T00:00:00Z"
	if err := fixture.database.UpsertHardwareResource(fixture.ctx, stored); err != nil {
		t.Fatal(err)
	}
	kept := decodePetHardwareView(t, petHardwareToolCall(t, fixture, "aha_update_hardware_resource",
		`{"resource_id":"hw-wireless","description":"手机（改名）"}`))
	if kept.ADBPairedAt == "" || !kept.Network.ADBWireless() || kept.Network.Port != 41235 {
		t.Fatalf("unrelated pet edit changed the wireless target: %#v", kept)
	}
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_update_hardware_resource",
		json.RawMessage(`{"resource_id":"hw-wireless","network":{"adb_connection":"usb"}}`)); err == nil ||
		!strings.Contains(err.Error(), "adb_serial") {
		t.Fatalf("switch to USB without serial: %v", err)
	}
	switched := decodePetHardwareView(t, petHardwareToolCall(t, fixture, "aha_update_hardware_resource",
		`{"resource_id":"hw-wireless","network":{"adb_connection":"usb","adb_serial":"SER2"}}`))
	if !switched.Network.ADBUSB() || switched.Network.Host != "" || switched.ADBPairedAt != "" {
		t.Fatalf("switch to USB: %#v", switched)
	}
	back := decodePetHardwareView(t, petHardwareToolCall(t, fixture, "aha_update_hardware_resource",
		`{"resource_id":"hw-wireless","network":{"adb_connection":"tcp","host":"192.0.2.5"}}`))
	if !back.Network.ADBTCP() || back.Network.ADBSerial != "" || back.Network.Port != 5555 {
		t.Fatalf("switch USB→tcp: %#v", back.Network)
	}
	audits := auditDataByAction(t, fixture.databasePath)
	if !strings.Contains(audits["pet.hardware_resource.update"], "network.adb_connection") {
		t.Fatalf("connection form change was not audited: %s", audits["pet.hardware_resource.update"])
	}
}
