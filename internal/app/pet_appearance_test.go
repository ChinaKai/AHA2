package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func petTestString(value string) *string { return &value }

// The "无形象" choice is stored in the existing character column. Turning it on
// must not disable the pet: state read/write, control and tool exposure all keep
// working, and switching back restores the previous kind of appearance.
func TestOwnerPetNoCharacterPersistsAndKeepsBackendRunning(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(database, secretStore, &stubExecutor{})
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })

	defaults, err := service.OwnerAssistant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Assistant.Character != "gugugaga" {
		t.Fatalf("default character = %q, want the existing default", defaults.Assistant.Character)
	}

	noAvatar, err := service.UpdateOwnerPetSettings(ctx, PetSettingsInput{
		Enabled: true, Character: assistantCharacterNone, Actions: []string{"walk", "jump"},
		IdleMinutes: 5, QuietStart: 23, QuietEnd: 8, HourlyLimit: 6,
	})
	if err != nil {
		t.Fatalf("enable no-character: %v", err)
	}
	if noAvatar.Assistant.Character != assistantCharacterNone || !noAvatar.Assistant.PetEnabled {
		t.Fatalf("settings after no-character = %#v", noAvatar.Assistant)
	}
	if len(noAvatar.Tools) == 0 {
		t.Fatal("pet tools disappeared in no-character mode")
	}

	reloaded, err := service.OwnerAssistant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Assistant.Character != assistantCharacterNone || !reloaded.Assistant.PetEnabled {
		t.Fatalf("persisted settings = %#v", reloaded.Assistant)
	}

	state, err := service.OwnerPetState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Character != assistantCharacterNone {
		t.Fatalf("pet state character = %q", state.Character)
	}
	state, err = service.UpdateOwnerPetState(ctx, domain.PetStatePatch{
		Action: petTestString("jump"), Mood: petTestString("happy"),
	})
	if err != nil {
		t.Fatalf("update pet state in no-character mode: %v", err)
	}
	if state.Action != "jump" || state.Mood != "happy" {
		t.Fatalf("pet state after update = %#v", state)
	}
	controlled, err := service.ControlOwnerPet(ctx, PetControlRequest{
		Actor: "owner", Actions: []PetBrainAction{{Type: "play", Name: "happy"}},
	})
	if err != nil || len(controlled.Actions) != 1 {
		t.Fatalf("control pet in no-character mode: actions=%#v err=%v", controlled.Actions, err)
	}

	// The quick character menu uses the assistant endpoint directly; it must
	// accept the same persisted value without disabling the pet.
	viaAssistant, err := service.UpdateOwnerAssistant(ctx, "", "", assistantCharacterNone)
	if err != nil || viaAssistant.Assistant.Character != assistantCharacterNone {
		t.Fatalf("assistant endpoint no-character: %#v err=%v", viaAssistant.Assistant, err)
	}

	restored, err := service.UpdateOwnerPetSettings(ctx, PetSettingsInput{
		Enabled: true, Character: "deepseek_fish", Actions: []string{"walk", "jump"},
		IdleMinutes: 5, QuietStart: 23, QuietEnd: 8, HourlyLimit: 6,
	})
	if err != nil {
		t.Fatalf("restore appearance: %v", err)
	}
	if restored.Assistant.Character != "deepseek_fish" || !restored.Assistant.PetEnabled {
		t.Fatalf("settings after restore = %#v", restored.Assistant)
	}
	state, err = service.OwnerPetState(ctx)
	if err != nil || state.Character != "deepseek_fish" {
		t.Fatalf("pet state after restore = %#v err=%v", state, err)
	}

	if _, err := service.UpdateOwnerPetSettings(ctx, PetSettingsInput{
		Enabled: true, Character: "not-a-character", Actions: []string{"walk"},
		HourlyLimit: 6, QuietStart: 23, QuietEnd: 8,
	}); err == nil {
		t.Fatal("invalid character was accepted")
	}
	if _, err := service.UpdateOwnerAssistant(ctx, "", "", "not-a-character"); err == nil {
		t.Fatal("invalid character was accepted by the assistant endpoint")
	}
}
