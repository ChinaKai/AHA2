package knowledgevault

import "testing"

func TestDefaultVaultEnabled(t *testing.T) {
	cases := []struct {
		name      string
		hasLegacy bool
		populated bool
		want      bool
	}{
		{"fresh install", false, false, true},
		{"fresh install already imported", false, true, true},
		{"upgrade before verification", true, false, false},
		{"upgrade after verification", true, true, true},
	}
	for _, item := range cases {
		if got := DefaultVaultEnabled(item.hasLegacy, item.populated); got != item.want {
			t.Errorf("%s: DefaultVaultEnabled(%v,%v) = %v, want %v", item.name, item.hasLegacy, item.populated, got, item.want)
		}
	}
}

func TestEvaluateReadinessStates(t *testing.T) {
	readyInput := ReadinessInput{
		VaultInitialized: true, VaultWritable: true,
		IndexReady: true, IndexWritable: true, EntryCount: 3,
	}
	t.Run("ready", func(t *testing.T) {
		result := EvaluateReadiness(readyInput)
		if result.Status != ReadinessReady || !result.CanEnable || !result.DefaultEnabled || result.SwitchVisible {
			t.Fatalf("fresh ready readiness = %+v", result)
		}
		if len(result.Missing) != 0 {
			t.Fatalf("ready missing = %v", result.Missing)
		}
	})
	t.Run("needs_rebuild", func(t *testing.T) {
		input := readyInput
		input.IndexReady = false
		result := EvaluateReadiness(input)
		if result.Status != ReadinessNeedsRebuild || result.CanEnable {
			t.Fatalf("missing index readiness = %+v", result)
		}
		if !hasCheck(result.Checks, "index_schema", checkFail) {
			t.Fatalf("missing index checks = %+v", result.Checks)
		}
	})
	t.Run("stale index needs rebuild", func(t *testing.T) {
		input := readyInput
		input.IndexStale = true
		result := EvaluateReadiness(input)
		if result.Status != ReadinessNeedsRebuild || result.CanEnable {
			t.Fatalf("stale index readiness = %+v", result)
		}
	})
	t.Run("needs_migration", func(t *testing.T) {
		input := readyInput
		input.LegacyEntries = 4
		input.LegacySkills = 2
		result := EvaluateReadiness(input)
		if result.Status != ReadinessNeedsMigration || result.CanEnable || !result.SwitchVisible || result.DefaultEnabled {
			t.Fatalf("upgrade readiness = %+v", result)
		}
		if result.Missing == nil || result.Missing[0] != "migration" {
			t.Fatalf("missing = %v, want migration", result.Missing)
		}
	})
	t.Run("verified migration clears the block", func(t *testing.T) {
		input := readyInput
		input.LegacyEntries = 4
		input.MigrationVerified = true
		result := EvaluateReadiness(input)
		if result.Status != ReadinessReady || !result.CanEnable || !result.DefaultEnabled {
			t.Fatalf("verified upgrade readiness = %+v", result)
		}
	})
	t.Run("error", func(t *testing.T) {
		input := readyInput
		input.VaultWritable = false
		result := EvaluateReadiness(input)
		if result.Status != ReadinessError || result.CanEnable {
			t.Fatalf("readonly readiness = %+v", result)
		}
	})
	t.Run("degraded unbound project", func(t *testing.T) {
		input := readyInput
		input.ProjectBindings = map[string]string{"bound": "bound"}
		input.KnownProjects = []string{"bound", "unbound"}
		result := EvaluateReadiness(input)
		if result.Status != ReadinessDegraded || !result.CanEnable {
			t.Fatalf("unbound readiness = %+v", result)
		}
		if len(result.UnboundProjects) != 1 || result.UnboundProjects[0] != "unbound" {
			t.Fatalf("unbound projects = %v", result.UnboundProjects)
		}
		if !hasCheck(result.Checks, "manifest_binding", checkWarn) {
			t.Fatalf("binding checks = %+v", result.Checks)
		}
	})
	t.Run("empty index is degraded", func(t *testing.T) {
		input := readyInput
		input.EntryCount = 0
		result := EvaluateReadiness(input)
		if result.Status != ReadinessDegraded || !result.CanEnable {
			t.Fatalf("empty index readiness = %+v", result)
		}
	})
	t.Run("unverified upgrade cannot enable even when otherwise ready", func(t *testing.T) {
		input := readyInput
		input.LegacyEntries = 1
		result := EvaluateReadiness(input)
		if result.CanEnable {
			t.Fatalf("unverified upgrade must not be enableable: %+v", result)
		}
	})
}
