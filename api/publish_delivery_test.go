package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNotificationAppsFromEnvironmentSupportsMultipleProfiles(t *testing.T) {
	t.Setenv(publishNotificationAppsEnv, `[{"id":"editor","name":"编辑应用","app_id":"cli_editor","app_secret":"secret-editor"},{"id":"ops","name":"运营应用","app_id":"cli_ops","app_secret":"secret-ops"}]`)
	apps, err := notificationAppsFromEnvironment()
	if err != nil {
		t.Fatalf("parse app profiles: %v", err)
	}
	if len(apps) != 2 || apps[0].ID != "editor" || apps[1].Name != "运营应用" {
		t.Fatalf("apps=%+v", apps)
	}

	for _, value := range []string{
		`[{"id":"bad id","name":"应用","app_id":"cli","app_secret":"secret"}]`,
		`[{"id":"editor","name":"应用","app_id":"cli","app_secret":"secret"},{"id":"editor","name":"重复","app_id":"cli2","app_secret":"secret2"}]`,
	} {
		t.Setenv(publishNotificationAppsEnv, value)
		if _, err := notificationAppsFromEnvironment(); err == nil {
			t.Fatalf("invalid app profile accepted: %s", value)
		}
	}
}

func TestPublishDeliverySelectionRequiresCurrentUserVisibleChats(t *testing.T) {
	apps := []feishuNotificationApp{{ID: "editor", Name: "编辑应用", AppID: "cli_editor", AppSecret: "secret-editor"}}
	chats := []feishuChat{{ChatID: "oc_allowed", Name: "内容审核群", ChatMode: "group"}}
	body := struct {
		AppProfileID string   `json:"app_profile_id"`
		ChatIDs      []string `json:"chat_ids"`
	}{AppProfileID: "editor", ChatIDs: []string{"oc_allowed"}}
	settings, err := publishDeliverySelectionFromRequest(body, publishDeliverySettings{}, apps, chats, "ou_reviewer")
	if err != nil {
		t.Fatalf("valid selection rejected: %v", err)
	}
	if settings.AppProfileID != "editor" || len(settings.ChatIDs) != 1 || settings.UpdatedBy != "ou_reviewer" {
		t.Fatalf("settings=%+v", settings)
	}

	body.ChatIDs = []string{"oc_not_visible"}
	if _, err := publishDeliverySelectionFromRequest(body, publishDeliverySettings{}, apps, chats, "ou_reviewer"); err == nil {
		t.Fatal("selection outside current user's chat list was accepted")
	}
}

func TestPublishDeliverySettingsStoreRoundTripsWithRestrictedFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "publish-delivery.json")
	store := newPublishDeliverySettingsStore(path)
	want := publishDeliverySettings{AppProfileID: "editor", ChatIDs: []string{"oc_allowed"}, UpdatedBy: "ou_reviewer", UpdatedAt: "2026-08-04T04:00:00Z"}
	if err := store.write(want); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	got, err := store.read()
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if got.AppProfileID != want.AppProfileID || len(got.ChatIDs) != 1 || got.UpdatedBy != want.UpdatedBy {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat settings: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode=%o, want 600", info.Mode().Perm())
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read raw settings: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(content, &raw); err != nil || raw["app_profile_id"] != "editor" {
		t.Fatalf("raw settings=%s err=%v", content, err)
	}
}
