package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFeishuDocumentExportMirrorsImagesInOneBatchAndReusesCache(t *testing.T) {
	entries := map[string][]byte{
		"word/document.xml":            []byte(`<w:document/>`),
		"word/_rels/document.xml.rels": []byte(`<Relationships/>`),
		"[Content_Types].xml":          []byte(`<Types/>`),
		"_rels/.rels":                  []byte(`<Relationships/>`),
	}
	for index := 1; index <= 12; index++ {
		entries[fmt.Sprintf("word/media/image%d.png", index)] = []byte{0x89, 0x50, 0x4e, 0x47, byte(index)}
	}
	docx := zipFixture(t, entries)

	uploadCalls := 0
	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploadCalls++
		var payload struct {
			Base64Images map[string]string `json:"base64_images"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upload payload: %v", err)
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		if len(payload.Base64Images) != 12 {
			t.Errorf("base64 images=%d, want 12", len(payload.Base64Images))
		}
		results := make(map[string]string, len(payload.Base64Images))
		for key, value := range payload.Base64Images {
			if !strings.HasPrefix(value, "data:image/png;base64,") {
				t.Errorf("upload image is not a PNG data URL: %q", value)
			}
			results[key] = "https://oss.example.com/feishu/" + key + ".png"
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	}))
	defer uploadServer.Close()
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", uploadServer.URL)

	provider := &fakeFeishuProvider{
		exportResult: feishuExportedFile{
			Filename: "十二图文章.docx",
			Content:  docx,
		},
	}
	service := newTestAuthService(t, provider)
	session := mustSessionForUser(t, service, authUser{
		ID:                     "feishu:ou_mirror_batch",
		Name:                   "张三",
		AuthMode:               authModeFeishu,
		Status:                 authUserActive,
		FeishuOpenID:           "ou_mirror_batch",
		FeishuRefreshTokenSeal: "test-refresh-seal",
	})
	service.putAccessToken("feishu:ou_mirror_batch", "u-test", service.now().Add(time.Hour))
	mux := registerTestAuthMux(service)
	body := []byte(`{"token":"doxcn_mirror_batch","type":"docx","title":"十二图文章"}`)

	for attempt := 1; attempt <= 2; attempt++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(
			rec,
			requestWithSession(http.MethodPost, "/api/feishu/docs/export", session, body),
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d export status=%d body=%s", attempt, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-FBIF-Images-Deferred"); got != "false" {
			t.Fatalf("attempt %d images deferred=%q, want false", attempt, got)
		}
		if got := rec.Header().Get("X-FBIF-Images-Mirrored"); got != "true" {
			t.Fatalf("attempt %d images mirrored=%q, want true", attempt, got)
		}
		if got := rec.Header().Get("X-FBIF-Image-Count"); got != "12" {
			t.Fatalf("attempt %d image count=%q, want 12", attempt, got)
		}

		imageURLs, err := readDocxImageURLMap(rec.Body.Bytes())
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if len(imageURLs) != 12 {
			t.Fatalf("attempt %d image URLs=%d, want 12", attempt, len(imageURLs))
		}
		for filename, value := range imageURLs {
			if !strings.HasPrefix(value, "https://oss.example.com/feishu/") {
				t.Fatalf("attempt %d %s URL=%q", attempt, filename, value)
			}
		}
	}

	if uploadCalls != 1 {
		t.Fatalf("upload calls=%d, want 1 batch reused by the second export", uploadCalls)
	}
}
