package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	body := publishDeliverySelectionRequest{AppProfileID: "editor", ChatIDs: []string{"oc_allowed"}, AutoPublish: true}
	settings, err := publishDeliverySelectionFromRequest(body, publishDeliverySettings{}, apps, chats, "ou_reviewer")
	if err != nil {
		t.Fatalf("valid selection rejected: %v", err)
	}
	if settings.AppProfileID != "editor" || len(settings.ChatIDs) != 1 || settings.UpdatedBy != "ou_reviewer" || !settings.AutoPublish {
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
	want := publishDeliverySettings{AppProfileID: "editor", ChatIDs: []string{"oc_allowed"}, AutoPublish: true, UpdatedBy: "ou_reviewer", UpdatedAt: "2026-08-04T04:00:00Z"}
	if err := store.write(want); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	got, err := store.read()
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if got.AppProfileID != want.AppProfileID || len(got.ChatIDs) != 1 || got.UpdatedBy != want.UpdatedBy || !got.AutoPublish {
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

func TestPublishDeliveryConfigurationBranches(t *testing.T) {
	t.Setenv(publishDeliverySettingsEnv, "  /tmp/explicit-delivery.json  ")
	if got := publishDeliverySettingsPath("/tmp/rules/rule-presets.json"); got != "/tmp/explicit-delivery.json" {
		t.Fatalf("explicit settings path=%q", got)
	}
	t.Setenv(publishDeliverySettingsEnv, "")
	if got := publishDeliverySettingsPath("/tmp/rules/rule-presets.json"); got != "/tmp/rules/publish-delivery.json" {
		t.Fatalf("rules-relative settings path=%q", got)
	}
	if got := publishDeliverySettingsPath(""); got != filepath.Join("data", "publish-delivery.json") {
		t.Fatalf("default settings path=%q", got)
	}

	for _, value := range []string{"", "bad.id", strings.Repeat("a", 257)} {
		if safePublishDeliveryID(value) {
			t.Fatalf("unsafe delivery id accepted: %q", value)
		}
	}
	if !safePublishDeliveryID("oc_valid-01") {
		t.Fatal("valid delivery id rejected")
	}
	normalized, err := normalizePublishChatIDs([]string{" oc_a ", "oc_a", "oc_b"})
	if err != nil || len(normalized) != 2 || normalized[0] != "oc_a" || normalized[1] != "oc_b" {
		t.Fatalf("normalized chat ids=%v err=%v", normalized, err)
	}
	tooMany := make([]string, maxPublishDeliveryChats+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("oc_%d", i)
	}
	if _, err := normalizePublishChatIDs(tooMany); err == nil {
		t.Fatal("more than the maximum number of chats was accepted")
	}
	settings, err := normalizePublishDeliverySettings(publishDeliverySettings{
		AppProfileID: " editor ", ChatIDs: []string{" oc_a "}, UpdatedBy: " reviewer ", UpdatedAt: " now ",
	})
	if err != nil || settings.SchemaVersion != publishDeliverySchema || settings.AppProfileID != "editor" || settings.UpdatedBy != "reviewer" {
		t.Fatalf("normalized settings=%+v err=%v", settings, err)
	}

	t.Setenv(publishNotificationAppsEnv, "")
	t.Setenv("FEISHU_APP_ID", " cli_default ")
	t.Setenv("FEISHU_APP_SECRET", " secret_default ")
	apps, err := notificationAppsFromEnvironment()
	if err != nil || len(apps) != 1 || apps[0].ID != "default" {
		t.Fatalf("default notification app=%+v err=%v", apps, err)
	}
	t.Setenv("FEISHU_APP_ID", "")
	t.Setenv("FEISHU_APP_SECRET", "")
	apps, err = notificationAppsFromEnvironment()
	if err != nil || len(apps) != 0 {
		t.Fatalf("unconfigured notification apps=%+v err=%v", apps, err)
	}
	t.Setenv(publishNotificationAppsEnv, `[{"id":"editor","name":"编辑应用","app_id":"cli_editor","app_secret":"secret"}]`)
	apps, err = notificationAppsFromEnvironment()
	if err != nil || len(apps) != 1 || apps[0].Name != "编辑应用" {
		t.Fatalf("structured notification apps=%+v err=%v", apps, err)
	}
	for _, raw := range []string{"not-json", `[]`, `[{"id":"editor","name":"","app_id":"cli","app_secret":"secret"}]`} {
		t.Setenv(publishNotificationAppsEnv, raw)
		if _, err := notificationAppsFromEnvironment(); err == nil {
			t.Fatalf("invalid notification app config accepted: %s", raw)
		}
	}

	t.Setenv("FEISHU_REVIEW_CHAT_ID", " oc_legacy ")
	legacy := legacyPublishReviewChatIDs()
	if len(legacy) != 1 || legacy[0] != "oc_legacy" {
		t.Fatalf("legacy chat ids=%v", legacy)
	}
	missing := newPublishDeliverySettingsStore(filepath.Join(t.TempDir(), "missing.json"))
	got, err := missing.read()
	if err != nil || len(got.ChatIDs) != 1 || got.ChatIDs[0] != "oc_legacy" {
		t.Fatalf("missing settings fallback=%+v err=%v", got, err)
	}
	emptyPath := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := newPublishDeliverySettingsStore(emptyPath).read(); err != nil || got.SchemaVersion != publishDeliverySchema {
		t.Fatalf("empty settings=%+v err=%v", got, err)
	}
	badPath := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(badPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newPublishDeliverySettingsStore(badPath).read(); err == nil {
		t.Fatal("malformed settings accepted")
	}

	t.Setenv(publishNotificationAppsEnv, `[{"id":"editor","name":"编辑应用","app_id":"cli_editor","app_secret":"secret"}]`)
	validPath := filepath.Join(t.TempDir(), "valid.json")
	validStore := newPublishDeliverySettingsStore(validPath)
	if err := validStore.write(publishDeliverySettings{AppProfileID: "editor", ChatIDs: []string{"oc_legacy"}}); err != nil {
		t.Fatal(err)
	}
	resolved, resolvedApps, err := validStore.resolve()
	if err != nil || resolved.AppProfileID != "editor" || len(resolvedApps) != 1 {
		t.Fatalf("resolved settings=%+v apps=%+v err=%v", resolved, resolvedApps, err)
	}
	if _, err := validStore.snapshot(); err != nil {
		t.Fatalf("valid settings snapshot failed: %v", err)
	}
	view := publishDeliverySettingsView(resolved, resolvedApps)
	if view["settings"] == nil || len(view["apps"].([]map[string]any)) != 1 {
		t.Fatalf("settings view=%+v", view)
	}
	if _, err := publishDeliverySelectionFromRequest(publishDeliverySelectionRequest{AppProfileID: "editor", ChatIDs: nil}, resolved, resolvedApps, []feishuChat{{ChatID: "oc_legacy"}}, "ou_reviewer"); err == nil {
		t.Fatal("empty chat selection accepted")
	}
	if err := validatePublishDeliveryChatSelection([]string{"oc_missing"}, []feishuChat{{ChatID: "oc_legacy"}}); err == nil {
		t.Fatal("unavailable chat selection accepted")
	}
	if err := validatePublishDeliveryChatSelection([]string{" oc_legacy ", "oc_legacy"}, []feishuChat{{ChatID: "oc_legacy"}}); err != nil {
		t.Fatalf("duplicate visible chat selection rejected: %v", err)
	}

	unknownPath := filepath.Join(t.TempDir(), "unknown.json")
	unknownStore := newPublishDeliverySettingsStore(unknownPath)
	if err := unknownStore.write(publishDeliverySettings{AppProfileID: "missing", ChatIDs: []string{"oc_legacy"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := unknownStore.resolve(); err == nil {
		t.Fatal("unknown app profile resolved")
	}
	noChatPath := filepath.Join(t.TempDir(), "no-chat.json")
	noChatStore := newPublishDeliverySettingsStore(noChatPath)
	if err := noChatStore.write(publishDeliverySettings{AppProfileID: "editor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := noChatStore.snapshot(); err == nil {
		t.Fatal("snapshot without chats succeeded")
	}
}

func TestPublishDeliveryJobTargetAndCardIDBranches(t *testing.T) {
	t.Setenv(publishNotificationAppsEnv, `[{"id":"editor","name":"编辑应用","app_id":"cli_editor","app_secret":"secret"}]`)
	t.Setenv("FEISHU_REVIEW_CHAT_ID", "oc_default")
	jobStore := newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json"))
	deliveryStore := newPublishDeliverySettingsStore(filepath.Join(t.TempDir(), "delivery.json"))
	if err := deliveryStore.write(publishDeliverySettings{AppProfileID: "editor", ChatIDs: []string{"oc_default"}}); err != nil {
		t.Fatal(err)
	}
	pipeline := &publishPipeline{store: jobStore, deliverySettings: deliveryStore}
	job, _, err := jobStore.create("site:delivery", "https://example.test/article")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.enqueueReviewNotification(&job)
	if job.ReviewDeliveryAppID != "editor" || len(job.ReviewDeliveryChatIDs) != 1 || job.NotificationKind != publishNotificationReviewPending {
		t.Fatalf("notification target=%+v", job)
	}

	explicit := job
	explicit.ReviewDeliveryAppID = "editor"
	explicit.ReviewDeliveryChatIDs = []string{"oc_explicit"}
	settings, apps, err := pipeline.reviewDeliveryForJob(explicit)
	if err != nil || settings.ChatIDs[0] != "oc_explicit" || len(apps) != 1 {
		t.Fatalf("explicit delivery=%+v apps=%+v err=%v", settings, apps, err)
	}
	if _, _, err := pipeline.reviewDeliveryForJob(publishJob{ReviewDeliveryAppID: "missing", ReviewDeliveryChatIDs: []string{"oc"}}); err == nil {
		t.Fatal("unknown job delivery app accepted")
	}
	if _, _, err := (&publishPipeline{}).reviewDeliveryForJob(job); err == nil {
		t.Fatal("pipeline without delivery settings accepted")
	}

	withIDs := publishJob{ReviewCardMsgID: "legacy", ReviewDeliveryChatIDs: []string{"oc_a", "oc_b"}, ReviewCardMsgIDs: map[string]string{"oc_b": "new-b"}}
	if got := reviewCardMessageID(withIDs, "oc_b"); got != "new-b" {
		t.Fatalf("per-chat card id=%q", got)
	}
	if got := reviewCardMessageID(withIDs, "oc_a"); got != "" {
		t.Fatalf("multi-chat legacy fallback=%q", got)
	}
	if got := reviewCardMessageID(publishJob{ReviewCardMsgID: "legacy"}, ""); got != "legacy" {
		t.Fatalf("legacy card id=%q", got)
	}
	// 切换审核群事故（2026-08-14 #78/#80）：map 时代任务的旧卡记录在旧群名下，
	// 目标切到新群后必须发新卡；不得回落旧单值字段把「更新旧群旧卡」当成已送达。
	switched := publishJob{
		ReviewCardMsgID:       "om_old_card",
		ReviewDeliveryChatIDs: []string{"oc_new_group"},
		ReviewCardMsgIDs:      map[string]string{"oc_old_group": "om_old_card"},
	}
	if got := reviewCardMessageID(switched, "oc_new_group"); got != "" {
		t.Fatalf("切群后新群必须视为未发过卡（应发新卡），实际返回 %q", got)
	}
	if got := reviewCardMessageID(switched, "oc_old_group"); got != "om_old_card" {
		t.Fatalf("旧群的卡仍应可原位更新，实际 %q", got)
	}
	if err := pipeline.rememberReviewCardMessageIDForChat(job, "oc_default", ""); err == nil {
		t.Fatal("empty card id accepted")
	}
	if err := pipeline.rememberReviewCardMessageIDForChat(job, "oc_default", "msg-1"); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.rememberReviewCardMessageIDForChat(job, "oc_default", "msg-2"); err != nil {
		t.Fatalf("stale duplicate card id should be harmless: %v", err)
	}
	stored, err := jobStore.byID(job.ID)
	if err != nil || stored.ReviewCardMsgIDs["oc_default"] != "msg-1" {
		t.Fatalf("stored card ids=%+v err=%v", stored.ReviewCardMsgIDs, err)
	}

	if _, err := pipeline.configuredReviewNotifier(settings, apps, "oc_default"); err != nil {
		t.Fatalf("configured notifier failed: %v", err)
	}
	if _, err := pipeline.configuredReviewNotifier(settings, nil, "oc_default"); err == nil {
		t.Fatal("notifier without app profile accepted")
	}
}

func TestPublishDeliveryFallbackPaths(t *testing.T) {
	t.Setenv(publishNotificationAppsEnv, `[{"id":"editor","name":"编辑应用","app_id":"cli_editor","app_secret":"secret"}]`)
	path := filepath.Join(t.TempDir(), "delivery.json")
	store := newPublishDeliverySettingsStore(path)
	if err := store.write(publishDeliverySettings{AppProfileID: "editor", ChatIDs: []string{"oc_review"}}); err != nil {
		t.Fatal(err)
	}
	pipeline := &publishPipeline{
		store:            newPublishJobStore(filepath.Join(t.TempDir(), "jobs.json")),
		deliverySettings: store,
	}
	job, _, err := pipeline.store.create("site:delivery-fallback", "https://example.test/fallback")
	if err != nil {
		t.Fatal(err)
	}
	if settings, apps, err := pipeline.reviewDeliveryForJob(job); err != nil || settings.AppProfileID != "editor" || len(apps) != 1 {
		t.Fatalf("configured delivery fallback=%+v apps=%+v err=%v", settings, apps, err)
	}
	if err := newPublishDeliverySettingsStore("").write(publishDeliverySettings{}); err == nil {
		t.Fatal("empty delivery settings path accepted")
	}
	if _, _, err := newPublishDeliverySettingsStore(filepath.Join(t.TempDir(), "empty.json")).snapshotWithApps(); err == nil {
		t.Fatal("delivery snapshot without chats accepted")
	}

	pipeline.coverImgKey = func(ctx context.Context, imageURL string) (string, error) {
		if imageURL != "https://example.test/cover.png" {
			t.Fatalf("cover URL=%q", imageURL)
		}
		return "img_key", nil
	}
	job.CoverURL = "https://example.test/cover.png"
	if got, err := (&publishPipeline{coverImgKey: pipeline.coverImgKey}).uploadReviewCoverForJob(context.Background(), job); err != nil || got != "img_key" {
		t.Fatalf("cover fallback=%q err=%v", got, err)
	}
	if _, err := (&publishPipeline{}).uploadReviewCoverForJob(context.Background(), job); err == nil {
		t.Fatal("missing cover uploader accepted")
	}

	if err := pipeline.rememberReviewCardMessageIDForChat(job, "", "legacy-message"); err != nil {
		t.Fatalf("empty chat should use legacy message id path: %v", err)
	}
	stored, err := pipeline.store.byID(job.ID)
	if err != nil || stored.ReviewCardMsgID != "legacy-message" {
		t.Fatalf("legacy message id=%+v err=%v", stored, err)
	}
}
