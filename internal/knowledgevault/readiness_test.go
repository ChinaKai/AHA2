package knowledgevault

import (
	"strings"
	"testing"
)

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
		if result.RollbackAllowed {
			t.Fatalf("a fresh install has no legacy knowledge to roll back to: %+v", result)
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
		if !result.RollbackAllowed {
			t.Fatalf("an upgrade that still owns unmigrated legacy knowledge keeps the rollback concept: %+v", result)
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
	t.Run("enabled in-use upgrade keeps legacy as a read-only backup", func(t *testing.T) {
		input := readyInput
		input.LegacyEntries = 4
		input.LegacySkills = 2
		input.Configured = true
		input.Enabled = true
		result := EvaluateReadiness(input)
		if result.Status != ReadinessReady || !result.CanEnable {
			t.Fatalf("in-use upgrade readiness = %+v", result)
		}
		if result.RollbackAllowed {
			t.Fatalf("an in-use upgrade must not allow a rollback: %+v", result)
		}
		if hasCheck(result.Checks, "migration", checkFail) {
			t.Fatalf("in-use upgrade must not report a migration failure: %+v", result.Checks)
		}
		if !hasCheck(result.Checks, "migration", checkOK) {
			t.Fatalf("migration check = %+v, want ok", result.Checks)
		}
		if result.Missing != nil {
			t.Fatalf("in-use upgrade missing = %v, want none", result.Missing)
		}
		if !strings.Contains(result.Message, "只读备份") {
			t.Fatalf("in-use message = %q, want a read-only-backup explanation", result.Message)
		}
	})
	t.Run("in-use upgrade with a degraded project still avoids migration", func(t *testing.T) {
		input := readyInput
		input.LegacyEntries = 4
		input.Configured = true
		input.Enabled = true
		input.ProjectBindings = map[string]string{"bound": "bound"}
		input.KnownProjects = []string{"bound", "unbound"}
		result := EvaluateReadiness(input)
		if result.Status != ReadinessDegraded || !result.CanEnable {
			t.Fatalf("in-use degraded readiness = %+v", result)
		}
		if hasCheck(result.Checks, "migration", checkFail) || !strings.Contains(result.Message, "只读备份") {
			t.Fatalf("in-use degraded must keep the read-only backup: %+v", result)
		}
		if result.RollbackAllowed {
			t.Fatalf("an in-use degraded upgrade must not allow a rollback: %+v", result)
		}
	})
	t.Run("configured but disabled upgrade still needs migration", func(t *testing.T) {
		input := readyInput
		input.LegacyEntries = 4
		input.Configured = true
		input.Enabled = false
		result := EvaluateReadiness(input)
		if result.Status != ReadinessNeedsMigration || result.CanEnable {
			t.Fatalf("disabled upgrade readiness = %+v", result)
		}
	})
	t.Run("enabled but unconfigured upgrade still needs migration", func(t *testing.T) {
		input := readyInput
		input.LegacyEntries = 4
		input.Configured = false
		input.Enabled = true
		result := EvaluateReadiness(input)
		if result.Status != ReadinessNeedsMigration || result.CanEnable {
			t.Fatalf("unconfigured upgrade readiness = %+v", result)
		}
	})
	t.Run("configured fresh install keeps the plain ready message", func(t *testing.T) {
		input := readyInput
		input.Configured = true
		input.Enabled = true
		result := EvaluateReadiness(input)
		if result.Status != ReadinessReady || !result.CanEnable {
			t.Fatalf("configured fresh install readiness = %+v", result)
		}
		if strings.Contains(result.Message, "只读备份") {
			t.Fatalf("fresh install message mentions a legacy backup: %q", result.Message)
		}
		if result.RollbackAllowed {
			t.Fatalf("a configured fresh install has no legacy knowledge to return to: %+v", result)
		}
	})
	t.Run("in-use upgrade with an empty index still needs migration", func(t *testing.T) {
		input := readyInput
		input.LegacyEntries = 4
		input.Configured = true
		input.Enabled = true
		input.EntryCount = 0
		result := EvaluateReadiness(input)
		if result.Status != ReadinessNeedsMigration || result.CanEnable {
			t.Fatalf("empty in-use upgrade readiness = %+v", result)
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

// rollback_allowed is the API contract the Web switch and the PUT handler both
// rely on: once the instance commits to the vault, a close must be refused.
func TestRollbackAllowedStates(t *testing.T) {
	base := ReadinessInput{
		VaultInitialized: true, VaultWritable: true,
		IndexReady: true, IndexWritable: true, EntryCount: 3,
	}
	build := func(mutate func(*ReadinessInput)) ReadinessInput {
		in := base
		mutate(&in)
		return in
	}
	cases := []struct {
		name string
		in   ReadinessInput
		want bool
	}{
		{"fresh install before any switch", base, false},
		{"fresh install after sample init", build(func(in *ReadinessInput) {
			in.Configured, in.Enabled = true, true
		}), false},
		{"upgrade still owning unmigrated legacy", build(func(in *ReadinessInput) {
			in.LegacyEntries = 4
		}), true},
		{"upgrade with verified migration", build(func(in *ReadinessInput) {
			in.LegacyEntries = 4
			in.MigrationVerified = true
		}), false},
		{"upgrade with verified import", build(func(in *ReadinessInput) {
			in.LegacyEntries = 4
			in.ImportVerified = true
		}), false},
		{"configured in-use upgrade", build(func(in *ReadinessInput) {
			in.LegacyEntries = 4
			in.Configured, in.Enabled = true, true
		}), false},
		{"configured upgrade that was switched off", build(func(in *ReadinessInput) {
			in.LegacyEntries = 4
			in.Configured = true
		}), false},
		{"configured instance after legacy cleanup", build(func(in *ReadinessInput) {
			in.Configured, in.Enabled = true, true
		}), false},
	}
	for _, item := range cases {
		if got := item.in.RollbackAllowed(); got != item.want {
			t.Errorf("%s: RollbackAllowed() = %v, want %v", item.name, got, item.want)
		}
		if got := EvaluateReadiness(item.in).RollbackAllowed; got != item.want {
			t.Errorf("%s: readiness.rollback_allowed = %v, want %v", item.name, got, item.want)
		}
	}
}
