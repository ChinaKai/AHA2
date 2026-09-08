package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const RuntimeProtocolVersion = 1

type Manifest struct {
	ManifestVersion       int      `json:"manifest_version"`
	PluginID              string   `json:"plugin_id"`
	ProviderKey           string   `json:"provider_key"`
	DisplayName           string   `json:"display_name"`
	PackageVersion        string   `json:"package_version"`
	ProtocolVersions      []string `json:"protocol_versions"`
	Endpoints             []string `json:"endpoints"`
	Executable            string   `json:"executable"`
	SHA256                string   `json:"sha256"`
	RequestedCapabilities []string `json:"requested_capabilities"`
}

func Discover(ctx context.Context, database *store.Store, root string, now time.Time) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return database.MarkMissingChannelPlugins(ctx, nil, now)
		}
		return fmt.Errorf("read channel plugin directory: %w", err)
	}
	seen := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(root, entry.Name(), "plugin.json")
		data, readErr := os.ReadFile(manifestPath)
		if readErr != nil {
			continue
		}
		var manifest Manifest
		if decodeErr := json.Unmarshal(data, &manifest); decodeErr != nil {
			continue
		}
		manifest.ProviderKey = strings.TrimSpace(manifest.ProviderKey)
		manifest.PluginID = strings.TrimSpace(manifest.PluginID)
		if manifest.ProviderKey == "" || manifest.PluginID == "" {
			continue
		}
		seen = append(seen, manifest.ProviderKey)
		item := validateManifest(manifest, manifestPath, data, now)
		if upsertErr := database.UpsertChannelPlugin(ctx, item); upsertErr != nil {
			return fmt.Errorf("store channel plugin %s: %w", manifest.ProviderKey, upsertErr)
		}
	}
	sort.Strings(seen)
	return database.MarkMissingChannelPlugins(ctx, seen, now)
}

func validateManifest(manifest Manifest, manifestPath string, raw []byte, now time.Time) domain.ChannelPlugin {
	item := domain.ChannelPlugin{
		ID: manifest.PluginID, ProviderKey: manifest.ProviderKey, DisplayName: strings.TrimSpace(manifest.DisplayName),
		ManifestVersion: manifest.ManifestVersion, PackageVersion: strings.TrimSpace(manifest.PackageVersion),
		ExecutableSHA256: strings.ToLower(strings.TrimSpace(manifest.SHA256)), InstallState: "invalid", Enabled: true,
		Revision: 1, DiscoveredAt: now, UpdatedAt: now,
	}
	if item.DisplayName == "" {
		item.DisplayName = item.ProviderKey
	}
	_ = json.Unmarshal(raw, &item.Manifest)
	protocolMin, protocolMax, protocolOK := protocolRange(manifest.ProtocolVersions)
	if protocolMin == 0 {
		protocolMin, protocolMax = 1, 1
	}
	item.ProtocolMin, item.ProtocolMax = protocolMin, protocolMax
	if manifest.ManifestVersion != 1 || manifest.PackageVersion == "" {
		item.LastError = "unsupported or incomplete plugin manifest"
		return item
	}
	if !protocolOK || RuntimeProtocolVersion < protocolMin || RuntimeProtocolVersion > protocolMax {
		item.InstallState = "incompatible"
		item.LastError = "channel runtime protocol is incompatible"
		return item
	}
	if !hasEndpoints(manifest.Endpoints) {
		item.LastError = "required channel endpoints are missing"
		return item
	}
	executable, err := resolvePluginExecutable(filepath.Dir(manifestPath), manifest.Executable)
	if err != nil {
		item.LastError = err.Error()
		return item
	}
	item.ExecutablePath = executable
	if item.ExecutableSHA256 == "" {
		item.LastError = "plugin executable sha256 is required"
		return item
	}
	digest, err := fileSHA256(executable)
	if err != nil {
		item.LastError = "plugin executable is missing or unreadable"
		return item
	}
	if digest != item.ExecutableSHA256 {
		item.LastError = "plugin executable sha256 mismatch"
		return item
	}
	item.InstallState = "installed"
	item.LastError = ""
	return item
}

func resolvePluginExecutable(root, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return "", fmt.Errorf("plugin executable must be a relative file")
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.Contains(clean, ":") {
		return "", fmt.Errorf("plugin executable escapes its package directory")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(rootAbs, clean))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("plugin executable escapes its package directory")
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("plugin executable is missing or unreadable")
	}
	return target, nil
}

func protocolRange(values []string) (int, int, bool) {
	min, max := 0, 0
	for _, value := range values {
		prefix, version, ok := strings.Cut(strings.TrimSpace(value), "/v")
		if !ok || prefix != "channel-runtime" {
			continue
		}
		number, err := strconv.Atoi(version)
		if err != nil || number < 1 {
			continue
		}
		if min == 0 || number < min {
			min = number
		}
		if number > max {
			max = number
		}
	}
	return min, max, min > 0
}

func hasEndpoints(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		seen[strings.TrimSpace(value)] = true
	}
	return seen[domain.ChannelEndpointAssistantDM] && seen[domain.ChannelEndpointGroupDigitalHuman]
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
