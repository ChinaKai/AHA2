package outboundproxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/proxyconfig"
)

const (
	subscriptionContentRef = "proxy/managed/subscription-content"
	subscriptionURLRef     = "proxy/managed/subscription-url"
	profileCollectionRef   = "proxy/managed/profiles-v1"
	maxManagedProfiles     = 20
)

type SettingsStore interface {
	ProxySettings(context.Context) (domain.ProxySettings, error)
	UpdateProxySettings(context.Context, domain.ProxySettings) (domain.ProxySettings, error)
}

type SecretStore interface {
	Get(string) (string, bool)
	PutMany(map[string]string) error
	DeleteMany([]string) error
}

type View struct {
	Configured       bool          `json:"configured"`
	ActiveProfileID  string        `json:"active_profile_id,omitempty"`
	Profiles         []ProfileView `json:"profiles"`
	URLConfigured    bool          `json:"url_configured"`
	Nodes            []NodeSummary `json:"nodes"`
	UnsupportedCount int           `json:"unsupported_count"`
	UnsupportedTypes []string      `json:"unsupported_types"`
	Status           string        `json:"status"`
	LastError        string        `json:"last_error,omitempty"`
}

type ProfileView struct {
	ID                     string        `json:"id"`
	Name                   string        `json:"name"`
	Active                 bool          `json:"active"`
	URLConfigured          bool          `json:"url_configured"`
	Nodes                  []NodeSummary `json:"nodes"`
	SelectedNodeID         string        `json:"selected_node_id"`
	ActiveNodeID           string        `json:"active_node_id,omitempty"`
	NeedsSelection         bool          `json:"needs_selection"`
	UnsupportedCount       int           `json:"unsupported_count"`
	UnsupportedTypes       []string      `json:"unsupported_types"`
	RefreshIntervalMinutes int           `json:"refresh_interval_minutes"`
	SubscriptionAt         time.Time     `json:"subscription_at,omitempty"`
}

type ProfilePatch struct {
	Name                   *string
	SelectedNodeID         *string
	RefreshIntervalMinutes *int
}

type ImportInput struct {
	ProfileID              string
	Name                   string
	Content                string
	URL                    string
	SelectedNodeID         string
	RefreshIntervalMinutes int
	Activate               bool
	AllowMissingSelection  bool
}

type storedProfile struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	Content                string    `json:"content"`
	URL                    string    `json:"url,omitempty"`
	SelectedNodeID         string    `json:"selected_node_id"`
	RefreshIntervalMinutes int       `json:"refresh_interval_minutes"`
	SubscriptionAt         time.Time `json:"subscription_at"`
}

type storedProfileCollection struct {
	Profiles []storedProfile `json:"profiles"`
}

type Controller struct {
	store      SettingsStore
	secrets    SecretStore
	direct     *http.Client
	profilesMu sync.RWMutex

	mu         sync.Mutex
	dialerInit sync.Mutex
	dialer     managedNodeDialer
	dialerID   string
	bridge     *proxyBridge
	status     string
	lastErr    string
	closed     bool
}

func New(store SettingsStore, secrets SecretStore) *Controller {
	return &Controller{
		store: store, secrets: secrets,
		direct: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many subscription redirects")
				}
				if request.URL.Scheme != "https" || len(via) == 0 || !strings.EqualFold(request.URL.Host, via[0].URL.Host) {
					return fmt.Errorf("subscription redirect changed origin")
				}
				return nil
			},
		}, status: "idle",
	}
}

func (c *Controller) loadProfiles(ctx context.Context) (storedProfileCollection, error) {
	if c.secrets == nil {
		return storedProfileCollection{}, fmt.Errorf("secret store unavailable")
	}
	if raw, ok := c.secrets.Get(profileCollectionRef); ok && raw != "" {
		if len(raw) > 16<<20 {
			return storedProfileCollection{}, fmt.Errorf("stored proxy profiles are oversized")
		}
		var collection storedProfileCollection
		if err := json.Unmarshal([]byte(raw), &collection); err != nil {
			return storedProfileCollection{}, fmt.Errorf("stored proxy profiles are invalid")
		}
		if len(collection.Profiles) > maxManagedProfiles {
			return storedProfileCollection{}, fmt.Errorf("too many stored proxy profiles")
		}
		seen := make(map[string]struct{}, len(collection.Profiles))
		for index := range collection.Profiles {
			profile := &collection.Profiles[index]
			if profile.ID == "" {
				return storedProfileCollection{}, fmt.Errorf("stored proxy profile is missing its ID")
			}
			if _, duplicate := seen[profile.ID]; duplicate {
				return storedProfileCollection{}, fmt.Errorf("stored proxy profiles contain duplicate IDs")
			}
			seen[profile.ID] = struct{}{}
			if profile.RefreshIntervalMinutes == 0 {
				profile.RefreshIntervalMinutes = 1440
			}
		}
		settings, settingsErr := c.store.ProxySettings(ctx)
		replacement, hasReplacement := firstUsableProfile(collection)
		if settingsErr == nil && settings.Mode == "managed_hysteria2" && profileIndex(collection, settings.ManagedProfileID) < 0 {
			if hasReplacement {
				settings.ManagedProfileID = replacement.ID
				settings.ManagedNodeID = replacement.SelectedNodeID
				settings.ManagedRefreshIntervalMins = replacement.RefreshIntervalMinutes
				settings.ManagedSubscriptionAt = replacement.SubscriptionAt
			} else {
				settings.Mode = "off"
				settings.ManagedProfileID = ""
				settings.ManagedNodeID = ""
				settings.ManagedSubscriptionAt = time.Time{}
			}
			settings.UpdatedAt = time.Now().UTC()
			if _, settingsErr = c.store.UpdateProxySettings(ctx, settings); settingsErr != nil {
				return storedProfileCollection{}, settingsErr
			}
		}
		return collection, nil
	}

	legacyContent, hasLegacy := c.secrets.Get(subscriptionContentRef)
	if !hasLegacy || strings.TrimSpace(legacyContent) == "" {
		return storedProfileCollection{}, nil
	}
	settings, err := c.store.ProxySettings(ctx)
	if err != nil {
		return storedProfileCollection{}, err
	}
	legacyURL, _ := c.secrets.Get(subscriptionURLRef)
	profile := storedProfile{
		ID: newProfileID(), Name: "默认配置", Content: legacyContent, URL: legacyURL,
		SelectedNodeID: settings.ManagedNodeID, RefreshIntervalMinutes: settings.ManagedRefreshIntervalMins,
		SubscriptionAt: settings.ManagedSubscriptionAt,
	}
	if profile.RefreshIntervalMinutes == 0 {
		profile.RefreshIntervalMinutes = 1440
	}
	collection := storedProfileCollection{Profiles: []storedProfile{profile}}
	encoded, err := json.Marshal(collection)
	if err != nil {
		return storedProfileCollection{}, err
	}
	if err := c.secrets.PutMany(map[string]string{profileCollectionRef: string(encoded)}); err != nil {
		return storedProfileCollection{}, fmt.Errorf("migrate managed proxy profile: %w", err)
	}
	settings.ManagedProfileID = profile.ID
	if _, err := c.store.UpdateProxySettings(ctx, settings); err != nil {
		_ = c.secrets.DeleteMany([]string{profileCollectionRef})
		return storedProfileCollection{}, err
	}
	_ = c.secrets.DeleteMany([]string{subscriptionContentRef, subscriptionURLRef})
	return collection, nil
}

func (c *Controller) saveProfiles(collection storedProfileCollection) error {
	if len(collection.Profiles) > maxManagedProfiles {
		return fmt.Errorf("最多保存 %d 个代理配置", maxManagedProfiles)
	}
	encoded, err := json.Marshal(collection)
	if err != nil {
		return err
	}
	if len(encoded) > 16<<20 {
		return fmt.Errorf("代理配置总大小超过 16 MiB")
	}
	if len(collection.Profiles) == 0 {
		return c.secrets.DeleteMany([]string{profileCollectionRef})
	}
	return c.secrets.PutMany(map[string]string{profileCollectionRef: string(encoded)})
}

func newProfileID() string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		now := time.Now().UTC().UnixNano()
		return fmt.Sprintf("proxy_profile_%x", now)
	}
	return "proxy_profile_" + hex.EncodeToString(value)
}

func profileIndex(collection storedProfileCollection, id string) int {
	for index := range collection.Profiles {
		if collection.Profiles[index].ID == id {
			return index
		}
	}
	return -1
}

func firstUsableProfile(collection storedProfileCollection) (storedProfile, bool) {
	for _, profile := range collection.Profiles {
		if subscription, err := ParseSubscription([]byte(profile.Content)); err == nil && len(subscription.Nodes) > 0 {
			return profile, true
		}
	}
	return storedProfile{}, false
}

func (c *Controller) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.profilesMu.Lock()
				collection, err := c.loadProfiles(ctx)
				c.profilesMu.Unlock()
				if err != nil {
					continue
				}
				for _, profile := range collection.Profiles {
					if profile.URL == "" {
						continue
					}
					interval := time.Duration(profile.RefreshIntervalMinutes) * time.Minute
					if interval <= 0 {
						interval = 24 * time.Hour
					}
					if profile.SubscriptionAt.IsZero() || time.Since(profile.SubscriptionAt) >= interval {
						_ = c.RefreshProfile(ctx, profile.ID)
					}
				}
			}
		}
	}()
}

func (c *Controller) View(ctx context.Context) View {
	c.profilesMu.Lock()
	defer c.profilesMu.Unlock()
	view := View{Status: c.runtimeStatus()}
	collection, err := c.loadProfiles(ctx)
	if err != nil {
		view.Status = "error"
		view.LastError = err.Error()
		return view
	}
	settings, err := c.store.ProxySettings(ctx)
	if err != nil {
		view.Status = "error"
		view.LastError = "读取代理设置失败"
		return view
	}
	view.Configured = len(collection.Profiles) > 0
	if settings.Mode == "managed_hysteria2" {
		view.ActiveProfileID = settings.ManagedProfileID
	}
	for _, profile := range collection.Profiles {
		profileView := ProfileView{
			ID: profile.ID, Name: profile.Name, Active: settings.Mode == "managed_hysteria2" && profile.ID == settings.ManagedProfileID,
			URLConfigured: profile.URL != "", SelectedNodeID: profile.SelectedNodeID,
			RefreshIntervalMinutes: profile.RefreshIntervalMinutes, SubscriptionAt: profile.SubscriptionAt,
		}
		if profileView.Active {
			profileView.ActiveNodeID = settings.ManagedNodeID
		}
		subscription, parseErr := ParseSubscription([]byte(profile.Content))
		if parseErr == nil || errors.Is(parseErr, ErrNoSupportedNodes) {
			profileView.Nodes = subscription.Summaries()
			if len(subscription.Nodes) > 0 {
				_, selectedExists := subscription.Node(profile.SelectedNodeID)
				profileView.NeedsSelection = !selectedExists
			} else if profileView.Active && profile.SelectedNodeID == "" {
				profileView.NeedsSelection = true
			}
			profileView.UnsupportedCount = subscription.UnsupportedCount
			profileView.UnsupportedTypes = append([]string(nil), subscription.UnsupportedTypes...)
			sort.Strings(profileView.UnsupportedTypes)
		}
		view.Profiles = append(view.Profiles, profileView)
		if profileView.Active {
			view.URLConfigured = profileView.URLConfigured
			view.Nodes = profileView.Nodes
			view.UnsupportedCount = profileView.UnsupportedCount
			view.UnsupportedTypes = profileView.UnsupportedTypes
		}
	}
	c.mu.Lock()
	view.LastError = c.lastErr
	c.mu.Unlock()
	return view
}

func (c *Controller) Import(ctx context.Context, input ImportInput) (domain.ProxySettings, error) {
	c.profilesMu.Lock()
	defer c.profilesMu.Unlock()
	return c.importLocked(ctx, input)
}

func (c *Controller) importLocked(ctx context.Context, input ImportInput) (domain.ProxySettings, error) {
	if c.secrets == nil {
		return domain.ProxySettings{}, fmt.Errorf("secret store unavailable")
	}
	content := strings.TrimSpace(input.Content)
	sourceURL := strings.TrimSpace(input.URL)
	if content == "" {
		if sourceURL == "" {
			return domain.ProxySettings{}, fmt.Errorf("subscription URL or YAML is required")
		}
		var err error
		content, err = c.fetchSubscription(ctx, sourceURL)
		if err != nil {
			return domain.ProxySettings{}, err
		}
	}
	subscription, err := ParseSubscription([]byte(content))
	unsupportedOnly := errors.Is(err, ErrNoSupportedNodes)
	if err != nil && !unsupportedOnly {
		return domain.ProxySettings{}, err
	}
	collection, err := c.loadProfiles(ctx)
	if err != nil {
		return domain.ProxySettings{}, err
	}
	settings, err := c.store.ProxySettings(ctx)
	if err != nil {
		return domain.ProxySettings{}, err
	}
	profileID := strings.TrimSpace(input.ProfileID)
	index := profileIndex(collection, profileID)
	if profileID == "" {
		if len(collection.Profiles) >= maxManagedProfiles {
			return domain.ProxySettings{}, fmt.Errorf("最多保存 %d 个代理配置", maxManagedProfiles)
		}
		profileID = newProfileID()
	}
	name := strings.TrimSpace(input.Name)
	if name == "" && index >= 0 {
		name = collection.Profiles[index].Name
	}
	if name == "" {
		name = fmt.Sprintf("代理配置 %d", len(collection.Profiles)+1)
	}
	if len(name) > 100 {
		return domain.ProxySettings{}, fmt.Errorf("代理配置名称不能超过 100 个字符")
	}
	nodeID := strings.TrimSpace(input.SelectedNodeID)
	if len(subscription.Nodes) == 0 {
		nodeID = ""
	} else if nodeID == "" {
		existingNodeID := settings.ManagedNodeID
		if index >= 0 {
			existingNodeID = collection.Profiles[index].SelectedNodeID
		}
		if _, ok := subscription.Node(existingNodeID); ok {
			nodeID = existingNodeID
		} else if input.AllowMissingSelection && index >= 0 {
			nodeID = ""
		} else {
			nodeID = subscription.Nodes[0].ID
		}
	}
	if len(subscription.Nodes) > 0 {
		if _, ok := subscription.Node(nodeID); !ok {
			if input.AllowMissingSelection && index >= 0 {
				nodeID = ""
			} else {
				return domain.ProxySettings{}, fmt.Errorf("selected node is not in the subscription")
			}
		}
	} else if index >= 0 && settings.ManagedProfileID == profileID && !input.AllowMissingSelection {
		return domain.ProxySettings{}, fmt.Errorf("当前使用中的配置不能替换为无可用节点的订阅")
	} else if nodeID != "" {
		return domain.ProxySettings{}, fmt.Errorf("selected node is not in the subscription")
	}
	if sourceURL != "" {
		if err := validateSubscriptionURL(sourceURL); err != nil {
			return domain.ProxySettings{}, err
		}
	}
	refreshMinutes := input.RefreshIntervalMinutes
	if refreshMinutes == 0 && index >= 0 {
		refreshMinutes = collection.Profiles[index].RefreshIntervalMinutes
	}
	if refreshMinutes == 0 {
		refreshMinutes = 1440
	}
	if refreshMinutes < 15 || refreshMinutes > 10080 {
		return domain.ProxySettings{}, fmt.Errorf("刷新间隔必须在 15 到 10080 分钟之间")
	}
	profile := storedProfile{
		ID: profileID, Name: name, Content: content, URL: sourceURL, SelectedNodeID: nodeID,
		RefreshIntervalMinutes: refreshMinutes, SubscriptionAt: time.Now().UTC(),
	}
	oldCollectionRaw, hadCollection := c.secrets.Get(profileCollectionRef)
	if index >= 0 {
		collection.Profiles[index] = profile
	} else {
		collection.Profiles = append(collection.Profiles, profile)
	}
	if err := c.saveProfiles(collection); err != nil {
		return domain.ProxySettings{}, fmt.Errorf("保存代理配置失败: %w", err)
	}
	activeChanged := len(subscription.Nodes) > 0 && input.Activate
	if activeChanged {
		settings.Mode = "managed_hysteria2"
		settings.ManagedProfileID = profileID
		settings.ManagedNodeID = nodeID
		settings.ManagedSubscriptionAt = profile.SubscriptionAt
		settings.ManagedRefreshIntervalMins = profile.RefreshIntervalMinutes
		settings, err = proxyconfig.Normalize(settings)
		if err == nil {
			settings.UpdatedAt = time.Now().UTC()
			settings, err = c.store.UpdateProxySettings(ctx, settings)
		}
	}
	if err != nil {
		if hadCollection {
			_ = c.secrets.PutMany(map[string]string{profileCollectionRef: oldCollectionRaw})
		} else {
			_ = c.secrets.DeleteMany([]string{profileCollectionRef})
		}
		return domain.ProxySettings{}, err
	}
	if activeChanged {
		c.invalidate()
	}
	return settings, nil
}

func (c *Controller) Refresh(ctx context.Context) error {
	settings, err := c.store.ProxySettings(ctx)
	if err != nil {
		return err
	}
	return c.RefreshProfile(ctx, settings.ManagedProfileID)
}

func (c *Controller) RefreshProfile(ctx context.Context, profileID string) error {
	c.profilesMu.Lock()
	defer c.profilesMu.Unlock()
	collection, err := c.loadProfiles(ctx)
	if err != nil {
		return err
	}
	index := profileIndex(collection, profileID)
	if index < 0 {
		return fmt.Errorf("代理配置不存在")
	}
	profile := collection.Profiles[index]
	if strings.TrimSpace(profile.URL) == "" {
		return fmt.Errorf("该代理配置没有订阅地址")
	}
	c.setStatus("refreshing", "")
	content, err := c.fetchSubscription(ctx, profile.URL)
	if err != nil {
		c.setStatus("error", "订阅刷新失败")
		return err
	}
	_, err = c.importLocked(ctx, ImportInput{
		ProfileID: profile.ID, Name: profile.Name, Content: content, URL: profile.URL,
		SelectedNodeID: profile.SelectedNodeID, RefreshIntervalMinutes: profile.RefreshIntervalMinutes,
		AllowMissingSelection: true,
	})
	if err != nil {
		c.setStatus("error", "订阅刷新失败")
		return err
	}
	c.setStatus("ready", "")
	return nil
}

func (c *Controller) PatchProfile(ctx context.Context, profileID string, patch ProfilePatch) (domain.ProxySettings, error) {
	c.profilesMu.Lock()
	defer c.profilesMu.Unlock()
	collection, err := c.loadProfiles(ctx)
	if err != nil {
		return domain.ProxySettings{}, err
	}
	index := profileIndex(collection, strings.TrimSpace(profileID))
	if index < 0 {
		return domain.ProxySettings{}, fmt.Errorf("代理配置不存在")
	}
	profile := collection.Profiles[index]
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" || len(name) > 100 {
			return domain.ProxySettings{}, fmt.Errorf("代理配置名称必须为 1 到 100 个字符")
		}
		profile.Name = name
	}
	if patch.RefreshIntervalMinutes != nil {
		interval := *patch.RefreshIntervalMinutes
		if interval < 15 || interval > 10080 {
			return domain.ProxySettings{}, fmt.Errorf("刷新间隔必须在 15 到 10080 分钟之间")
		}
		profile.RefreshIntervalMinutes = interval
	}
	if patch.SelectedNodeID != nil {
		nodeID := strings.TrimSpace(*patch.SelectedNodeID)
		subscription, parseErr := ParseSubscription([]byte(profile.Content))
		if parseErr != nil {
			return domain.ProxySettings{}, parseErr
		}
		if _, ok := subscription.Node(nodeID); !ok {
			return domain.ProxySettings{}, fmt.Errorf("请选择有效的代理节点")
		}
		profile.SelectedNodeID = nodeID
	}
	oldRaw, hadOld := c.secrets.Get(profileCollectionRef)
	collection.Profiles[index] = profile
	if err := c.saveProfiles(collection); err != nil {
		return domain.ProxySettings{}, err
	}
	settings, err := c.store.ProxySettings(ctx)
	if err != nil {
		if hadOld {
			_ = c.secrets.PutMany(map[string]string{profileCollectionRef: oldRaw})
		}
		return domain.ProxySettings{}, err
	}
	if settings.ManagedProfileID == profile.ID && patch.RefreshIntervalMinutes != nil {
		settings.ManagedRefreshIntervalMins = profile.RefreshIntervalMinutes
		settings.UpdatedAt = time.Now().UTC()
		settings, err = c.store.UpdateProxySettings(ctx, settings)
		if err != nil && hadOld {
			_ = c.secrets.PutMany(map[string]string{profileCollectionRef: oldRaw})
		}
	}
	return settings, err
}

func (c *Controller) ActivateProfile(ctx context.Context, profileID string) (domain.ProxySettings, error) {
	c.profilesMu.Lock()
	defer c.profilesMu.Unlock()
	collection, err := c.loadProfiles(ctx)
	if err != nil {
		return domain.ProxySettings{}, err
	}
	index := profileIndex(collection, strings.TrimSpace(profileID))
	if index < 0 {
		return domain.ProxySettings{}, fmt.Errorf("代理配置不存在")
	}
	profile := collection.Profiles[index]
	subscription, err := ParseSubscription([]byte(profile.Content))
	if err != nil {
		return domain.ProxySettings{}, err
	}
	if _, ok := subscription.Node(profile.SelectedNodeID); !ok {
		return domain.ProxySettings{}, fmt.Errorf("请先选择有效的代理节点")
	}
	settings, err := c.store.ProxySettings(ctx)
	if err != nil {
		return domain.ProxySettings{}, err
	}
	settings.Mode = "managed_hysteria2"
	settings.ManagedProfileID = profile.ID
	settings.ManagedNodeID = profile.SelectedNodeID
	settings.ManagedRefreshIntervalMins = profile.RefreshIntervalMinutes
	settings.ManagedSubscriptionAt = profile.SubscriptionAt
	settings.UpdatedAt = time.Now().UTC()
	settings, err = c.store.UpdateProxySettings(ctx, settings)
	if err != nil {
		return domain.ProxySettings{}, err
	}
	c.invalidate()
	return settings, nil
}

func (c *Controller) DeleteProfile(ctx context.Context, profileID string) (domain.ProxySettings, error) {
	c.profilesMu.Lock()
	defer c.profilesMu.Unlock()
	collection, err := c.loadProfiles(ctx)
	if err != nil {
		return domain.ProxySettings{}, err
	}
	index := profileIndex(collection, strings.TrimSpace(profileID))
	if index < 0 {
		return domain.ProxySettings{}, fmt.Errorf("代理配置不存在")
	}
	oldRaw, hadOld := c.secrets.Get(profileCollectionRef)
	collection.Profiles = append(collection.Profiles[:index], collection.Profiles[index+1:]...)
	if err := c.saveProfiles(collection); err != nil {
		return domain.ProxySettings{}, err
	}
	settings, err := c.store.ProxySettings(ctx)
	if err != nil {
		if hadOld {
			_ = c.secrets.PutMany(map[string]string{profileCollectionRef: oldRaw})
		}
		return domain.ProxySettings{}, err
	}
	activeDeleted := settings.ManagedProfileID == profileID
	if activeDeleted {
		replacement, hasReplacement := firstUsableProfile(collection)
		if !hasReplacement {
			settings.Mode = "off"
			settings.ManagedProfileID = ""
			settings.ManagedNodeID = ""
			settings.ManagedSubscriptionAt = time.Time{}
		} else {
			settings.ManagedProfileID = replacement.ID
			settings.ManagedNodeID = replacement.SelectedNodeID
			settings.ManagedRefreshIntervalMins = replacement.RefreshIntervalMinutes
			settings.ManagedSubscriptionAt = replacement.SubscriptionAt
		}
		settings.UpdatedAt = time.Now().UTC()
	}
	settings, err = c.store.UpdateProxySettings(ctx, settings)
	if err != nil {
		if hadOld {
			_ = c.secrets.PutMany(map[string]string{profileCollectionRef: oldRaw})
		}
		return domain.ProxySettings{}, err
	}
	if activeDeleted {
		c.invalidate()
	}
	return settings, nil
}

func (c *Controller) UpdateSettings(ctx context.Context, input domain.ProxySettings) (domain.ProxySettings, error) {
	c.profilesMu.Lock()
	defer c.profilesMu.Unlock()
	current, err := c.store.ProxySettings(ctx)
	if err != nil {
		return domain.ProxySettings{}, err
	}
	if input.ManagedProfileID == "" {
		input.ManagedProfileID = current.ManagedProfileID
	}
	if input.ManagedNodeID == "" {
		input.ManagedNodeID = current.ManagedNodeID
	}
	input.ManagedSubscriptionAt = current.ManagedSubscriptionAt
	input, err = proxyconfig.Normalize(input)
	if err != nil {
		return domain.ProxySettings{}, err
	}
	var oldProfilesRaw string
	var hadProfiles bool
	if input.Mode == "managed_hysteria2" {
		collection, err := c.loadProfiles(ctx)
		if err != nil {
			return domain.ProxySettings{}, err
		}
		index := profileIndex(collection, input.ManagedProfileID)
		if index < 0 {
			return domain.ProxySettings{}, fmt.Errorf("请选择有效的代理配置")
		}
		profile := collection.Profiles[index]
		if input.ManagedNodeID == "" || input.ManagedProfileID != current.ManagedProfileID {
			input.ManagedNodeID = profile.SelectedNodeID
		}
		subscription, err := ParseSubscription([]byte(profile.Content))
		if err != nil {
			return domain.ProxySettings{}, err
		}
		if _, ok := subscription.Node(input.ManagedNodeID); !ok {
			return domain.ProxySettings{}, fmt.Errorf("请选择有效的代理节点")
		}
		profile.SelectedNodeID = input.ManagedNodeID
		profile.RefreshIntervalMinutes = input.ManagedRefreshIntervalMins
		collection.Profiles[index] = profile
		oldProfilesRaw, hadProfiles = c.secrets.Get(profileCollectionRef)
		if err := c.saveProfiles(collection); err != nil {
			return domain.ProxySettings{}, err
		}
		input.ManagedSubscriptionAt = profile.SubscriptionAt
	}
	input.UpdatedAt = time.Now().UTC()
	updated, err := c.store.UpdateProxySettings(ctx, input)
	if err != nil && input.Mode == "managed_hysteria2" {
		if hadProfiles {
			_ = c.secrets.PutMany(map[string]string{profileCollectionRef: oldProfilesRaw})
		}
	}
	if err == nil {
		c.invalidate()
	}
	return updated, err
}

func (c *Controller) Client(ctx context.Context, base *http.Client) (*http.Client, error) {
	settings, err := c.store.ProxySettings(ctx)
	if err != nil {
		return nil, err
	}
	return c.ClientFor(ctx, base, settings)
}

// ServiceClient applies the built-in tunnel to AHA-owned outbound requests.
// Legacy external proxy settings remain opt-in through the existing Task and
// Codex account switches, so upgrading does not silently route control-plane
// requests through the historical 127.0.0.1 preset.
func (c *Controller) ServiceClient(ctx context.Context, base *http.Client) (*http.Client, error) {
	settings, err := c.store.ProxySettings(ctx)
	if err != nil {
		return nil, err
	}
	if settings.Mode != "managed_hysteria2" {
		return cloneDirectClient(base, nil), nil
	}
	return c.ClientFor(ctx, base, settings)
}

func (c *Controller) ClientFor(ctx context.Context, base *http.Client, settings domain.ProxySettings) (*http.Client, error) {
	normalized, err := proxyconfig.Normalize(settings)
	if err != nil {
		return nil, err
	}
	if normalized.Mode == "off" {
		return cloneDirectClient(base, nil), nil
	}
	if normalized.Mode == "external" {
		return proxyconfig.Client(base, normalized)
	}
	current, err := c.store.ProxySettings(ctx)
	if err != nil {
		return nil, err
	}
	if current.ManagedProfileID != normalized.ManagedProfileID || current.ManagedNodeID != normalized.ManagedNodeID {
		return nil, fmt.Errorf("save the managed node selection before testing")
	}
	dialer, err := c.managedDialer(ctx, normalized.ManagedProfileID, normalized.ManagedNodeID)
	if err != nil {
		return nil, err
	}
	return cloneDirectClient(base, dialer.DialContext), nil
}

// ClientForNode creates an isolated client for testing a saved node without
// changing or interrupting the active proxy profile.
func (c *Controller) ClientForNode(ctx context.Context, base *http.Client, profileID, nodeID string) (*http.Client, func(), error) {
	node, err := c.managedNode(ctx, strings.TrimSpace(profileID), strings.TrimSpace(nodeID))
	if err != nil {
		return nil, nil, err
	}
	dialer, err := newManagedNodeDialer(node)
	if err != nil {
		return nil, nil, err
	}
	client := cloneDirectClient(base, dialer.DialContext)
	cleanup := func() {
		client.CloseIdleConnections()
		_ = dialer.Close()
	}
	return client, cleanup, nil
}

func (c *Controller) ApplyEnvironment(ctx context.Context, environment map[string]string) error {
	settings, err := c.store.ProxySettings(ctx)
	if err != nil {
		return err
	}
	settings, err = proxyconfig.Normalize(settings)
	if err != nil {
		return err
	}
	if settings.Mode != "managed_hysteria2" {
		proxyconfig.ApplyEnvironment(environment, settings)
		return nil
	}
	bridge, err := c.ensureBridge(ctx, settings.ManagedProfileID, settings.ManagedNodeID)
	if err != nil {
		return err
	}
	proxyconfig.ApplyEnvironment(environment, domain.ProxySettings{
		Mode: "external", HTTPProxy: bridge.URL(), HTTPSProxy: bridge.URL(), NoProxy: settings.NoProxy,
		ManagedRefreshIntervalMins: settings.ManagedRefreshIntervalMins,
	})
	return nil
}

func (c *Controller) fetchSubscription(ctx context.Context, sourceURL string) (string, error) {
	if err := validateSubscriptionURL(sourceURL); err != nil {
		return "", err
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	request.Header.Set("User-Agent", "Clash.Meta")
	request.Header.Set("Accept", "application/yaml,text/yaml,text/plain,*/*")
	response, err := c.direct.Do(request)
	if err != nil {
		return "", fmt.Errorf("download subscription failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("download subscription failed: HTTP %d", response.StatusCode)
	}
	reader := io.LimitReader(response.Body, maxSubscriptionBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("read subscription failed")
	}
	if len(data) > maxSubscriptionBytes {
		return "", fmt.Errorf("subscription exceeds 4 MiB")
	}
	return string(data), nil
}

func validateSubscriptionURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" {
		return fmt.Errorf("subscription URL must use https://")
	}
	if parsed.User != nil {
		return fmt.Errorf("subscription URL credentials must be provided by its query token")
	}
	return nil
}

func (c *Controller) profileSubscription(ctx context.Context, profileID string) (Subscription, error) {
	c.profilesMu.Lock()
	defer c.profilesMu.Unlock()
	collection, err := c.loadProfiles(ctx)
	if err != nil {
		return Subscription{}, err
	}
	index := profileIndex(collection, profileID)
	if index < 0 {
		return Subscription{}, fmt.Errorf("请选择有效的代理配置")
	}
	return ParseSubscription([]byte(collection.Profiles[index].Content))
}

func (c *Controller) managedDialer(ctx context.Context, profileID, nodeID string) (managedNodeDialer, error) {
	node, err := c.managedNode(ctx, profileID, nodeID)
	if err != nil {
		return nil, err
	}
	c.dialerInit.Lock()
	defer c.dialerInit.Unlock()
	return c.managedDialerLocked(profileID, nodeID, node)
}

func (c *Controller) managedNode(ctx context.Context, profileID, nodeID string) (Node, error) {
	subscription, err := c.profileSubscription(ctx, profileID)
	if err != nil {
		return Node{}, err
	}
	node, ok := subscription.Node(nodeID)
	if !ok {
		return Node{}, fmt.Errorf("selected managed node is unavailable")
	}
	return node, nil
}

func (c *Controller) managedDialerLocked(profileID, nodeID string, node Node) (managedNodeDialer, error) {
	dialerID := profileID + "\x00" + nodeID
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("managed proxy is closed")
	}
	if c.dialer != nil && c.dialerID == dialerID {
		dialer := c.dialer
		c.mu.Unlock()
		return dialer, nil
	}
	c.mu.Unlock()
	dialer, err := newManagedNodeDialer(node)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = dialer.Close()
		return nil, errors.New("managed proxy is closed")
	}
	if c.dialer != nil && c.dialerID == dialerID {
		existing := c.dialer
		c.mu.Unlock()
		_ = dialer.Close()
		return existing, nil
	}
	old := c.dialer
	oldBridge := c.bridge
	c.dialer = dialer
	c.dialerID = dialerID
	c.bridge = nil
	c.status = "ready"
	c.lastErr = ""
	c.mu.Unlock()
	if oldBridge != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = oldBridge.Close(closeCtx)
		cancel()
	}
	if old != nil {
		_ = old.Close()
	}
	return dialer, nil
}

func (c *Controller) ensureBridge(ctx context.Context, profileID, nodeID string) (*proxyBridge, error) {
	node, err := c.managedNode(ctx, profileID, nodeID)
	if err != nil {
		return nil, err
	}
	c.dialerInit.Lock()
	defer c.dialerInit.Unlock()
	dialer, err := c.managedDialerLocked(profileID, nodeID, node)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bridge != nil {
		return c.bridge, nil
	}
	bridge, err := newProxyBridge(dialer.DialContext)
	if err != nil {
		return nil, err
	}
	c.bridge = bridge
	return bridge, nil
}

func (c *Controller) invalidate() {
	c.dialerInit.Lock()
	defer c.dialerInit.Unlock()
	c.mu.Lock()
	dialer, bridge := c.dialer, c.bridge
	c.dialer, c.bridge, c.dialerID = nil, nil, ""
	c.status, c.lastErr = "idle", ""
	c.mu.Unlock()
	if bridge != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = bridge.Close(ctx)
		cancel()
	}
	if dialer != nil {
		_ = dialer.Close()
	}
}

func (c *Controller) runtimeStatus() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Controller) setStatus(status, lastErr string) {
	c.mu.Lock()
	c.status, c.lastErr = status, lastErr
	c.mu.Unlock()
}

func (c *Controller) Close() error {
	c.dialerInit.Lock()
	defer c.dialerInit.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	dialer, bridge := c.dialer, c.bridge
	c.dialer, c.bridge = nil, nil
	c.mu.Unlock()
	var errs []error
	if bridge != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		errs = append(errs, bridge.Close(ctx))
		cancel()
	}
	if dialer != nil {
		errs = append(errs, dialer.Close())
	}
	return errors.Join(errs...)
}

func cloneDirectClient(base *http.Client, dial dialContextFunc) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if dial != nil {
		transport.DialContext = dial
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	if base != nil {
		client.Timeout = base.Timeout
		client.CheckRedirect = base.CheckRedirect
		client.Jar = base.Jar
	}
	return client
}
