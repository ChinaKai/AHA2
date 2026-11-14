package knowledgevault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestManager(t *testing.T) (*Manager, Config) {
	t.Helper()
	config := DefaultConfig(t.TempDir())
	manager, err := NewManager(config, "devbox")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, config
}

func TestManagerSwitchToleratesMissingVault(t *testing.T) {
	ctx := context.Background()
	manager, _ := newTestManager(t)
	if manager.Enabled() {
		t.Fatal("manager starts enabled")
	}
	if err := manager.WithReader(func(*Reader) error { return nil }); !errors.Is(err, ErrDisabled) {
		t.Fatalf("WithReader while disabled = %v, want ErrDisabled", err)
	}
	if err := manager.SetEnabled(ctx, true); err != nil {
		t.Fatalf("enabling an empty vault: %v", err)
	}
	status := manager.Status(ctx)
	if !status.Enabled || status.VaultReady || status.IndexReady || status.VaultInitialized {
		t.Fatalf("status = %+v, want enabled with an empty vault", status)
	}
	if status.Warning != "vault_missing" {
		t.Fatalf("warning = %q, want vault_missing", status.Warning)
	}
	var libraries LibraryList
	if err := manager.WithReader(func(reader *Reader) error {
		libraries = reader.Libraries(ctx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !libraries.Enabled || libraries.VaultReady || libraries.Warning != "vault_missing" {
		t.Fatalf("libraries = %+v, want the existing empty state", libraries)
	}
	if err := manager.SetEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	if manager.Enabled() {
		t.Fatal("manager still enabled after SetEnabled(false)")
	}
}

func TestManagerInitializeSeedsSamplesAndRebuilds(t *testing.T) {
	ctx := context.Background()
	manager, config := newTestManager(t)
	result, err := manager.Initialize(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Samples != 4 || len(result.Created) != 4 {
		t.Fatalf("samples = %d created=%v, want 4", result.Samples, result.Created)
	}
	if !manager.Enabled() {
		t.Fatal("Initialize did not enable the manager")
	}
	status := manager.Status(ctx)
	if !status.Enabled || !status.VaultReady || !status.VaultInitialized || !status.IndexReady {
		t.Fatalf("status after init = %+v", status)
	}
	if status.EntryCount < 5 {
		t.Fatalf("entry count = %d, want at least the four samples plus global/index.md", status.EntryCount)
	}
	var doc Doc
	if err := manager.WithReader(func(reader *Reader) error {
		var readErr error
		doc, readErr = reader.Document(ctx, SampleWelcomeID, "")
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Body, "文件是知识正本") {
		t.Fatalf("sample body = %q", doc.Body)
	}
	for _, sample := range SampleDocuments(time.Now()) {
		path := filepath.Join(config.VaultPath, filepath.FromSlash(sample.Path))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("sample %s missing: %v", sample.Path, err)
		}
	}
}

func TestManagerInitializeRejectsExistingVaultWithoutChangingIt(t *testing.T) {
	ctx := context.Background()
	manager, config := newTestManager(t)
	if _, err := manager.Initialize(ctx, true); err != nil {
		t.Fatal(err)
	}
	samplePath := filepath.Join(config.VaultPath, filepath.FromSlash(SampleDocuments(time.Now())[0].Path))
	before, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Initialize(ctx, true); !errors.Is(err, ErrVaultExists) {
		t.Fatalf("second Initialize = %v, want ErrVaultExists", err)
	}
	after, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("Initialize overwrote an existing sample file")
	}
}

func TestManagerRebuildPicksUpEditedFiles(t *testing.T) {
	ctx := context.Background()
	manager, config := newTestManager(t)
	if _, err := manager.Initialize(ctx, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.VaultPath, "global", "general", "示例", "欢迎使用.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "文件是知识正本", "文件是唯一正本", 1)
	if edited == string(raw) {
		t.Fatal("sample body did not contain the expected sentence")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := manager.Rebuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Entries == 0 {
		t.Fatal("rebuild reported no entries")
	}
	var doc Doc
	if err := manager.WithReader(func(reader *Reader) error {
		var readErr error
		doc, readErr = reader.Document(ctx, SampleWelcomeID, "")
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Body, "文件是唯一正本") {
		t.Fatalf("rebuilt body = %q", doc.Body)
	}
	// Rebuild before the vault exists is refused; rebuild while off is refused.
	emptyManager, _ := newTestManager(t)
	if _, err := emptyManager.Rebuild(ctx); !errors.Is(err, ErrDisabled) {
		t.Fatalf("rebuild while disabled = %v, want ErrDisabled", err)
	}
	if err := emptyManager.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := emptyManager.Rebuild(ctx); !errors.Is(err, ErrVaultNotInitialized) {
		t.Fatalf("rebuild on empty vault = %v, want ErrVaultNotInitialized", err)
	}
}

func TestManagerConcurrentReadsAndToggles(t *testing.T) {
	ctx := context.Background()
	manager, _ := newTestManager(t)
	if _, err := manager.Initialize(ctx, true); err != nil {
		t.Fatal(err)
	}

	const readers = 6
	const iterations = 25
	var wg sync.WaitGroup
	errCh := make(chan error, readers*iterations)
	for index := 0; index < readers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < iterations; round++ {
				err := manager.WithReader(func(reader *Reader) error {
					_ = reader.Libraries(ctx)
					_, readErr := reader.Document(ctx, SampleWelcomeID, "")
					return readErr
				})
				// The toggler may switch the vault off between iterations; that
				// is the expected 404 path, not a race failure.
				if err != nil && !errors.Is(err, ErrDisabled) {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for round := 0; round < 12; round++ {
			if err := manager.SetEnabled(ctx, false); err != nil {
				errCh <- err
				return
			}
			if err := manager.SetEnabled(ctx, true); err != nil {
				errCh <- err
				return
			}
			if round%6 == 5 {
				if _, err := manager.Rebuild(ctx); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent access failed: %v", err)
	}
	if !manager.Enabled() {
		t.Fatal("manager ended disabled after the toggle loop")
	}
}

func TestEffectiveStartupSwitchPrefersDatabaseAfterFirstWebToggle(t *testing.T) {
	cases := []struct {
		name           string
		configured     bool
		storedEnabled  bool
		startupDefault bool
		wantEnabled    bool
		wantSource     string
	}{
		{"unconfigured env off", false, false, false, false, SourceStartup},
		{"unconfigured env on", false, false, true, true, SourceStartup},
		{"configured database on beats env off", true, true, false, true, SourceDatabase},
		{"configured database off beats env on", true, false, true, false, SourceDatabase},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			enabled, source := EffectiveStartupSwitch(testCase.configured, testCase.storedEnabled, testCase.startupDefault)
			if enabled != testCase.wantEnabled || source != testCase.wantSource {
				t.Fatalf("EffectiveStartupSwitch = (%v,%q), want (%v,%q)", enabled, source, testCase.wantEnabled, testCase.wantSource)
			}
		})
	}
}
