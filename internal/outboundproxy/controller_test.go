package outboundproxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestVLESSRealityProfileActivatesAndProvidesLoopbackBridge(t *testing.T) {
	ctx := context.Background()
	settingsStore := &memorySettingsStore{settings: domain.ProxySettings{Mode: "external", ManagedRefreshIntervalMins: 1440}}
	secrets := &memorySecrets{values: map[string]string{}}
	controller := New(settingsStore, secrets)
	defer controller.Close()
	publicKey := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	raw := `proxies:
  - name: vless-reality
    type: vless
    server: 127.0.0.1
    port: 1
    uuid: 00000000-0000-4000-8000-000000000001
    network: tcp
    tls: true
    flow: xtls-rprx-vision
    servername: cover.example.invalid
    client-fingerprint: chrome
    reality-opts:
      public-key: ` + publicKey + `
      short-id: 0123abcd
`
	settings, err := controller.Import(ctx, ImportInput{Name: "VLESS", Content: raw, Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "managed_hysteria2" || settings.ManagedNodeID == "" {
		t.Fatalf("VLESS profile was not activated: %#v", settings)
	}
	view := controller.View(ctx)
	if len(view.Nodes) != 1 || view.Nodes[0].Protocol != "vless" {
		t.Fatalf("VLESS summary missing: %#v", view)
	}
	environment := map[string]string{}
	if err := controller.ApplyEnvironment(ctx, environment); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
		if value := environment[key]; !strings.HasPrefix(value, "http://aha:") || !strings.Contains(value, "@127.0.0.1:") {
			t.Fatalf("%s is not an authenticated loopback bridge: %q", key, value)
		}
	}
}

type memorySettingsStore struct {
	mu       sync.Mutex
	settings domain.ProxySettings
}

func TestSubscriptionURLRefreshMarksMissingSelectionWithoutSwitchingTraffic(t *testing.T) {
	store := &memorySettingsStore{settings: domain.ProxySettings{
		Mode: "external", NoProxy: "localhost", ManagedRefreshIntervalMins: 1440,
	}}
	secrets := &memorySecrets{values: map[string]string{}}
	content := `proxies: [{name: first, type: hysteria2, server: first.example, port: 443, password: first-secret}]`
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.UserAgent() != "Clash.Meta" {
			t.Errorf("unexpected subscription user agent: %q", request.UserAgent())
		}
		_, _ = writer.Write([]byte(content))
	}))
	defer server.Close()
	controller := New(store, secrets)
	controller.direct = server.Client()
	defer controller.Close()
	settings, err := controller.Import(context.Background(), ImportInput{URL: server.URL, Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	firstID := settings.ManagedNodeID
	content = `proxies: [{name: second, type: hysteria2, server: second.example, port: 443, password: second-secret}]`
	if err := controller.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	settings, _ = store.ProxySettings(context.Background())
	if settings.ManagedNodeID != firstID {
		t.Fatalf("refresh silently switched the active node: %#v", settings)
	}
	view := controller.View(context.Background())
	if len(view.Nodes) != 1 || view.Nodes[0].Name != "second" || !view.URLConfigured {
		t.Fatalf("unexpected refreshed view: %#v", view)
	}
	if len(view.Profiles) != 1 || view.Profiles[0].SelectedNodeID != "" || !view.Profiles[0].NeedsSelection || view.Profiles[0].ActiveNodeID != firstID {
		t.Fatalf("missing selection was not staged for explicit action: %#v", view.Profiles)
	}
	if _, err := controller.ActivateProfile(context.Background(), view.Profiles[0].ID); err == nil {
		t.Fatal("profile with a missing selection was activated")
	}
	secondID := view.Nodes[0].ID
	if _, err := controller.PatchProfile(context.Background(), view.Profiles[0].ID, ProfilePatch{SelectedNodeID: &secondID}); err != nil {
		t.Fatal(err)
	}
	settings, err = controller.ActivateProfile(context.Background(), view.Profiles[0].ID)
	if err != nil || settings.ManagedNodeID != secondID {
		t.Fatalf("explicitly selected replacement node was not activated: settings=%#v err=%v", settings, err)
	}
	if _, err := controller.Import(context.Background(), ImportInput{URL: "http://example.invalid/subscription"}); err == nil {
		t.Fatal("expected insecure subscription URL to be rejected")
	}
}

func TestProfileNodeSelectionIsStagedUntilExplicitActivation(t *testing.T) {
	ctx := context.Background()
	store := &memorySettingsStore{settings: domain.ProxySettings{Mode: "external", ManagedRefreshIntervalMins: 1440}}
	secrets := &memorySecrets{values: map[string]string{}}
	controller := New(store, secrets)
	defer controller.Close()
	_, err := controller.Import(ctx, ImportInput{
		Name: "multi-node",
		Content: `proxies:
  - {name: first, type: hysteria2, server: first.example, port: 443, password: first-secret}
  - {name: second, type: hysteria2, server: second.example, port: 443, password: second-secret}
`,
		Activate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	view := controller.View(ctx)
	profile := view.Profiles[0]
	activeNodeID := profile.ActiveNodeID
	secondNodeID := profile.Nodes[1].ID
	if activeNodeID == "" || activeNodeID == secondNodeID {
		t.Fatalf("unexpected initial node state: %#v", profile)
	}
	patched, err := controller.PatchProfile(ctx, profile.ID, ProfilePatch{SelectedNodeID: &secondNodeID})
	if err != nil {
		t.Fatal(err)
	}
	if patched.ManagedNodeID != activeNodeID {
		t.Fatalf("selecting a node switched traffic immediately: %#v", patched)
	}
	view = controller.View(ctx)
	if view.Profiles[0].SelectedNodeID != secondNodeID || view.Profiles[0].ActiveNodeID != activeNodeID {
		t.Fatalf("staged and active nodes were not kept separate: %#v", view.Profiles[0])
	}
	activated, err := controller.ActivateProfile(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if activated.ManagedNodeID != secondNodeID || activated.ManagedProfileID != profile.ID {
		t.Fatalf("explicit activation did not apply staged node: %#v", activated)
	}
}

func (s *memorySettingsStore) ProxySettings(context.Context) (domain.ProxySettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings, nil
}

func (s *memorySettingsStore) UpdateProxySettings(_ context.Context, settings domain.ProxySettings) (domain.ProxySettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = settings
	return settings, nil
}

type memorySecrets struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *memorySecrets) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	return value, ok
}

func (s *memorySecrets) PutMany(values map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, value := range values {
		s.values[key] = value
	}
	return nil
}

func (s *memorySecrets) DeleteMany(keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		delete(s.values, key)
	}
	return nil
}

func TestImportPersistsSubscriptionOnlyInSecretStore(t *testing.T) {
	store := &memorySettingsStore{settings: domain.ProxySettings{
		Mode: "external", NoProxy: "localhost", ManagedRefreshIntervalMins: 1440,
	}}
	secrets := &memorySecrets{values: map[string]string{}}
	controller := New(store, secrets)
	defer controller.Close()
	raw := `proxies:
  - name: safe-name
    type: hysteria2
    server: secret-server.example
    port: 443
    password: secret-auth
    obfs: salamander
    obfs-password: secret-obfs
`
	settings, err := controller.Import(context.Background(), ImportInput{Content: raw, RefreshIntervalMinutes: 60, Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "managed_hysteria2" || settings.ManagedProfileID == "" || settings.ManagedNodeID == "" || settings.ManagedRefreshIntervalMins != 60 {
		t.Fatalf("unexpected managed settings: %#v", settings)
	}
	if settings.ManagedSubscriptionAt.IsZero() || time.Since(settings.ManagedSubscriptionAt) > time.Minute {
		t.Fatalf("unexpected subscription timestamp: %v", settings.ManagedSubscriptionAt)
	}
	stored, _ := store.ProxySettings(context.Background())
	if strings.Contains(stored.ManagedNodeID, "secret") || stored.HTTPProxy == "secret-auth" {
		t.Fatalf("secret escaped into regular settings: %#v", stored)
	}
	secretContent, ok := secrets.Get(profileCollectionRef)
	if !ok || !strings.Contains(secretContent, "secret-auth") {
		t.Fatal("subscription was not saved to the secret store")
	}
	var collection storedProfileCollection
	if err := json.Unmarshal([]byte(secretContent), &collection); err != nil || len(collection.Profiles) != 1 {
		t.Fatalf("invalid stored profile collection: %v", err)
	}
	view := controller.View(context.Background())
	if !view.Configured || len(view.Profiles) != 1 || len(view.Nodes) != 1 || view.Nodes[0].Name != "safe-name" {
		t.Fatalf("unexpected view: %#v", view)
	}

	environment := map[string]string{}
	if err := controller.ApplyEnvironment(context.Background(), environment); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
		value := environment[key]
		if !strings.HasPrefix(value, "http://aha:") || !strings.Contains(value, "@127.0.0.1:") {
			t.Fatalf("%s is not an authenticated loopback bridge: %q", key, value)
		}
	}
}

func TestMultipleProfilesCanActivateAndDelete(t *testing.T) {
	ctx := context.Background()
	store := &memorySettingsStore{settings: domain.ProxySettings{Mode: "external", ManagedRefreshIntervalMins: 1440}}
	secrets := &memorySecrets{values: map[string]string{}}
	controller := New(store, secrets)
	defer controller.Close()
	firstYAML := `proxies: [{name: first-node, type: hysteria2, server: first.example, port: 443, password: first-secret}]`
	first, err := controller.Import(ctx, ImportInput{Name: "first-profile", Content: firstYAML, Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	secondYAML := `proxies: [{name: second-node, type: hysteria2, server: second.example, port: 443, password: second-secret}]`
	current, err := controller.Import(ctx, ImportInput{Name: "second-profile", Content: secondYAML})
	if err != nil {
		t.Fatal(err)
	}
	view := controller.View(ctx)
	if len(view.Profiles) != 2 || view.ActiveProfileID != first.ManagedProfileID || current.ManagedProfileID != first.ManagedProfileID {
		t.Fatalf("unexpected profiles after import: %#v", view)
	}
	secondProfile := view.Profiles[1]
	activated, err := controller.UpdateSettings(ctx, domain.ProxySettings{
		Mode: "managed_hysteria2", ManagedProfileID: secondProfile.ID,
		ManagedNodeID: secondProfile.SelectedNodeID, ManagedRefreshIntervalMins: secondProfile.RefreshIntervalMinutes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if activated.ManagedProfileID != secondProfile.ID {
		t.Fatalf("second profile was not explicitly activated: %#v", activated)
	}
	afterDelete, err := controller.DeleteProfile(ctx, secondProfile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterDelete.ManagedProfileID != first.ManagedProfileID || afterDelete.Mode != "managed_hysteria2" {
		t.Fatalf("active profile did not fall back: %#v", afterDelete)
	}
	afterDelete, err = controller.DeleteProfile(ctx, first.ManagedProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if afterDelete.Mode != "off" || afterDelete.ManagedProfileID != "" || afterDelete.ManagedNodeID != "" {
		t.Fatalf("last profile deletion did not disable managed proxy: %#v", afterDelete)
	}
	if view := controller.View(ctx); view.Configured || len(view.Profiles) != 0 {
		t.Fatalf("profiles remain after deletion: %#v", view)
	}
}

func TestClientForNodeDoesNotSwitchActiveProfile(t *testing.T) {
	ctx := context.Background()
	store := &memorySettingsStore{settings: domain.ProxySettings{Mode: "external", ManagedRefreshIntervalMins: 1440}}
	secrets := &memorySecrets{values: map[string]string{}}
	controller := New(store, secrets)
	defer controller.Close()
	first, err := controller.Import(ctx, ImportInput{
		Name: "active", Content: `proxies: [{name: active-node, type: hysteria2, server: active.example, port: 443, password: active-secret}]`, Activate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Import(ctx, ImportInput{
		Name: "candidate", Content: `proxies: [{name: candidate-node, type: hysteria2, server: candidate.example, port: 443, password: candidate-secret}]`,
	}); err != nil {
		t.Fatal(err)
	}
	view := controller.View(ctx)
	candidate := view.Profiles[1]
	client, cleanup, err := controller.ClientForNode(ctx, nil, candidate.ID, candidate.SelectedNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if client == nil || cleanup == nil {
		t.Fatal("node test client was not created")
	}
	cleanup()
	settings, _ := store.ProxySettings(ctx)
	if settings.ManagedProfileID != first.ManagedProfileID || settings.ManagedNodeID != first.ManagedNodeID {
		t.Fatalf("node test switched the active profile: %#v", settings)
	}
}

func TestLegacySubscriptionMigratesToDefaultProfile(t *testing.T) {
	ctx := context.Background()
	raw := `proxies: [{name: legacy-node, type: hysteria2, server: legacy.example, port: 443, password: legacy-secret}]`
	subscription, err := ParseSubscription([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	store := &memorySettingsStore{settings: domain.ProxySettings{
		Mode: "managed_hysteria2", ManagedNodeID: subscription.Nodes[0].ID, ManagedRefreshIntervalMins: 1440,
	}}
	secrets := &memorySecrets{values: map[string]string{subscriptionContentRef: raw}}
	controller := New(store, secrets)
	defer controller.Close()
	view := controller.View(ctx)
	if len(view.Profiles) != 1 || view.Profiles[0].Name != "默认配置" || view.ActiveProfileID == "" {
		t.Fatalf("legacy profile was not migrated: %#v", view)
	}
	if _, ok := secrets.Get(subscriptionContentRef); ok {
		t.Fatal("legacy subscription secret was not removed")
	}
}

func TestUnsupportedSubscriptionCanBeStoredButNotActivated(t *testing.T) {
	ctx := context.Background()
	store := &memorySettingsStore{settings: domain.ProxySettings{Mode: "external", ManagedRefreshIntervalMins: 1440}}
	secrets := &memorySecrets{values: map[string]string{}}
	controller := New(store, secrets)
	defer controller.Close()
	unsupported := `proxies: [{name: future-node, type: vless, server: example.invalid, port: 443, uuid: 00000000-0000-4000-8000-000000000001}]`
	settings, err := controller.Import(ctx, ImportInput{Name: "future-profile", Content: unsupported})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "external" || settings.ManagedProfileID != "" {
		t.Fatalf("unsupported profile was activated: %#v", settings)
	}
	view := controller.View(ctx)
	if len(view.Profiles) != 1 || view.Profiles[0].Name != "future-profile" || len(view.Profiles[0].Nodes) != 0 || view.Profiles[0].UnsupportedCount != 1 {
		t.Fatalf("unsupported profile was not retained safely: %#v", view)
	}
	if _, err := controller.UpdateSettings(ctx, domain.ProxySettings{
		Mode: "managed_hysteria2", ManagedProfileID: view.Profiles[0].ID, ManagedRefreshIntervalMins: 1440,
	}); err == nil {
		t.Fatal("unsupported profile was activated")
	}
}

func TestManagedModeDoesNotImplicitlySelectTheFirstProfile(t *testing.T) {
	ctx := context.Background()
	store := &memorySettingsStore{settings: domain.ProxySettings{Mode: "external", ManagedRefreshIntervalMins: 1440}}
	secrets := &memorySecrets{values: map[string]string{}}
	controller := New(store, secrets)
	defer controller.Close()
	if _, err := controller.Import(ctx, ImportInput{
		Name: "saved", Content: `proxies: [{name: node, type: hysteria2, server: example.invalid, port: 443, password: secret}]`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.UpdateSettings(ctx, domain.ProxySettings{Mode: "managed_hysteria2", ManagedRefreshIntervalMins: 1440}); err == nil {
		t.Fatal("managed mode implicitly selected the first saved profile")
	}
	view := controller.View(ctx)
	if view.ActiveProfileID != "" || view.Profiles[0].Active {
		t.Fatalf("inactive saved profile was presented as active: %#v", view)
	}
}

func TestConcurrentBridgeAndSettingsUpdatesDoNotDeadlock(t *testing.T) {
	ctx := context.Background()
	store := &memorySettingsStore{settings: domain.ProxySettings{Mode: "external", ManagedRefreshIntervalMins: 1440}}
	secrets := &memorySecrets{values: map[string]string{}}
	controller := New(store, secrets)
	defer controller.Close()
	settings, err := controller.Import(ctx, ImportInput{Content: `proxies: [{name: node, type: hysteria2, server: example.invalid, port: 443, password: secret}]`, Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() {
		for range 20 {
			if err := controller.ApplyEnvironment(ctx, map[string]string{}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	go func() {
		for range 20 {
			if _, err := controller.UpdateSettings(ctx, settings); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	timeout := time.After(10 * time.Second)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-timeout:
			t.Fatal("concurrent bridge/settings update deadlocked")
		}
	}
}
