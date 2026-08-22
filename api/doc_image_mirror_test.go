package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
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

func preparedMirrorFixture(t *testing.T, imageSizes map[string]int) (hash string, filenames []string, optimized []byte) {
	t.Helper()
	entries := map[string][]byte{
		"word/document.xml":            []byte(`<w:document/>`),
		"word/_rels/document.xml.rels": []byte(`<Relationships/>`),
		"[Content_Types].xml":          []byte(`<Types/>`),
		"_rels/.rels":                  []byte(`<Relationships/>`),
	}
	for name, size := range imageSizes {
		if size < 3 {
			t.Fatalf("image %s too small", name)
		}
		data := make([]byte, size)
		data[0], data[1], data[2] = 0x89, 0x50, 0x4e
		entries["word/media/"+name] = data
	}
	original := zipFixture(t, entries)
	optimized, err := stripImagesFromDocx(original)
	if err != nil {
		t.Fatal(err)
	}
	imageURLs, err := readDocxImageURLMap(optimized)
	if err != nil {
		t.Fatal(err)
	}
	filenames = make([]string, 0, len(imageURLs))
	for filename := range imageURLs {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)
	sum := sha256.Sum256(original)
	return hex.EncodeToString(sum[:])[:16], filenames, optimized
}

func startMirrorUploadServer(t *testing.T) (*int, *[]int) {
	t.Helper()
	uploadCalls := 0
	batchSizes := []int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploadCalls++
		var payload struct {
			Base64Images map[string]string `json:"base64_images"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upload payload: %v", err)
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		batchSizes = append(batchSizes, len(payload.Base64Images))
		results := make(map[string]string, len(payload.Base64Images))
		for key := range payload.Base64Images {
			results[key] = "https://oss.example.com/feishu/" + key + ".png"
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	}))
	t.Cleanup(server.Close)
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", server.URL)
	return &uploadCalls, &batchSizes
}

func TestMirrorCachedDocxImagesSplitsBatchesUnderPayloadCap(t *testing.T) {
	uploadCalls, batchSizes := startMirrorUploadServer(t)
	hash, filenames, optimized := preparedMirrorFixture(t, map[string]int{
		"a.png": 10,
		"b.png": 10,
		"c.png": 10,
	})
	rewritten, count, err := mirrorCachedDocxImagesToOSSWithLimits(
		context.Background(), hash, filenames, optimized, 60, 25,
	)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("mirrored count=%d, want 3", count)
	}
	if *uploadCalls != 2 {
		t.Fatalf("upload calls=%d, want 2 batches", *uploadCalls)
	}
	if len(*batchSizes) != 2 || (*batchSizes)[0] != 2 || (*batchSizes)[1] != 1 {
		t.Fatalf("batch sizes=%v, want [2 1]", *batchSizes)
	}
	imageURLs, err := readDocxImageURLMap(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	if len(imageURLs) != 3 {
		t.Fatalf("image URLs=%d, want 3", len(imageURLs))
	}
}

func TestMirrorCachedDocxImagesKeepsOversizedSingleImageInOwnBatch(t *testing.T) {
	uploadCalls, batchSizes := startMirrorUploadServer(t)
	hash, filenames, optimized := preparedMirrorFixture(t, map[string]int{
		"wide.png": 40,
	})
	if _, count, err := mirrorCachedDocxImagesToOSSWithLimits(
		context.Background(), hash, filenames, optimized, 60, 8,
	); err != nil {
		t.Fatal(err)
	} else if count != 1 {
		t.Fatalf("mirrored count=%d, want 1", count)
	}
	if *uploadCalls != 1 || len(*batchSizes) != 1 || (*batchSizes)[0] != 1 {
		t.Fatalf("calls=%d sizes=%v, want one single-image batch", *uploadCalls, *batchSizes)
	}
}

func TestMirrorCachedDocxImagesRejectsWhenTotalExceedsLimit(t *testing.T) {
	startMirrorUploadServer(t)
	hash, filenames, optimized := preparedMirrorFixture(t, map[string]int{
		"a.png": 10,
		"b.png": 10,
		"c.png": 10,
	})
	_, _, err := mirrorCachedDocxImagesToOSSWithLimits(
		context.Background(), hash, filenames, optimized, 20, 25,
	)
	if err == nil || !strings.Contains(err.Error(), "exceed direct mirror limit") {
		t.Fatalf("err=%v, want total-size rejection", err)
	}
}
