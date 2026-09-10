package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestReauthorizationOnlyAddsScopes(t *testing.T) {
	t.Parallel()
	options := registrationOptions(command{Payload: map[string]any{"create_only": false, "app_id": "existing-app", "app_name": "do-not-rename", "minimal_preset": true}})
	if options.CreateOnly || options.AppID != "existing-app" || options.AppPreset != nil || options.Addons.Preset != nil {
		t.Fatal("reauthorization must target the same app without applying creation presets")
	}
	if !reflect.DeepEqual(options.Addons.Scopes.Tenant, registrationTenantScopes()) || len(options.Addons.Scopes.User) != 0 {
		t.Fatal("reauthorization scopes changed")
	}
	if len(options.Addons.Events.Items.Tenant) != 0 || len(options.Addons.Events.Items.User) != 0 || len(options.Addons.Callbacks.Items) != 0 {
		t.Fatal("permission update must not configure events or callbacks")
	}
	initial := registrationOptions(command{Payload: map[string]any{"create_only": true, "app_name": "new-app"}})
	if !initial.CreateOnly || initial.AppPreset == nil || initial.Addons.Preset == nil || *initial.Addons.Preset || len(initial.Addons.Events.Items.Tenant) != 2 || len(initial.Addons.Callbacks.Items) != 1 {
		t.Fatal("new app registration lost its initial configuration")
	}
}

func TestLegacyMenuCommandsPreserveCustomMenusWithoutProviderRequests(t *testing.T) {
	t.Parallel()
	menus := []string{"owner-menu-one", "owner-menu-two"}
	client := lark.NewClient("existing-menu", "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		t.Errorf("legacy menu command made an unexpected provider request: %s", request.URL.Path)
		menus = nil
		return mediaTestResponse(500, "{}"), nil
	}}))
	for _, attempts := range []int{1, 2, 12} {
		result, err := configureMenuCommand(context.Background(), client, "existing-menu", command{Kind: "configure_menu", Attempts: attempts, Payload: map[string]any{"version": 6}})
		if err != nil || result["status"] != "skipped" || result["reason"] != "existing_menu_preserved" {
			t.Fatalf("result=%v err=%v", result, err)
		}
	}
	if !reflect.DeepEqual(menus, []string{"owner-menu-one", "owner-menu-two"}) {
		t.Fatal("custom menus changed")
	}
}

func TestMenuInitializationRejectsReplaysAndUnscopedCommands(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"retry", "expired", "missing_time", "future", "other_app", "missing_onboarding", "not_new"} {
		t.Run(name, func(t *testing.T) {
			item := command{Kind: "initialize_menu", Attempts: 1, CreatedAt: time.Now().Add(-time.Second), Payload: map[string]any{"new_app": true, "app_id": "new-app", "onboarding_id": "onboarding"}}
			switch name {
			case "retry":
				item.Attempts = 2
			case "expired":
				item.CreatedAt = time.Now().Add(-6 * time.Minute)
			case "missing_time":
				item.CreatedAt = time.Time{}
			case "future":
				item.CreatedAt = time.Now().Add(time.Minute)
			case "other_app":
				item.Payload["app_id"] = "other-app"
			case "missing_onboarding":
				delete(item.Payload, "onboarding_id")
			case "not_new":
				item.Payload["new_app"] = false
			}
			if _, err := configureMenuCommand(context.Background(), nil, "new-app", item); err == nil || menuConfigurationErrorCode(err) != "menu_confirmation_required" {
				t.Fatal("unsafe initialization accepted")
			}
		})
	}
}

func TestMenuInitializationDoesNotFallbackOnRejectedMenu(t *testing.T) {
	t.Parallel()
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "rejected"}[rejected], func(t *testing.T) {
			paths := []string{}
			client := lark.NewClient("initial-menu", "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
				if strings.Contains(request.URL.Path, "/auth/") {
					return mediaTestResponse(200, `{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
				}
				paths = append(paths, request.URL.Path)
				if strings.HasSuffix(request.URL.Path, "/config") {
					var payload map[string]any
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					scope, ok := payload["scope"].(map[string]any)
					if !ok || scope["add_scopes"] == nil || scope["remove_scopes"] != nil {
						t.Fatal("scope configuration must be additive")
					}
				}
				if strings.HasSuffix(request.URL.Path, "/ability") && rejected {
					return mediaTestResponse(200, `{"code":210011,"msg":"invalid menu"}`), nil
				}
				return mediaTestResponse(200, `{"code":0}`), nil
			}}))
			item := command{Kind: "initialize_menu", Attempts: 1, CreatedAt: time.Now().Add(-time.Second), Payload: map[string]any{"new_app": true, "app_id": "initial-menu", "onboarding_id": "onboarding"}}
			result, err := configureMenuCommand(context.Background(), client, "initial-menu", item)
			if rejected {
				if err == nil || len(paths) != 2 || !strings.HasSuffix(paths[1], "/ability") {
					t.Fatalf("rejected menu must not fallback or publish: paths=%v err=%v", paths, err)
				}
			} else if err != nil || len(paths) != 3 || result["status"] != "publish_submitted" {
				t.Fatalf("initial menu: paths=%v result=%v err=%v", paths, result, err)
			}
		})
	}
}
