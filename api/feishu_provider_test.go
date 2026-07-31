package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNormalizeFeishuTokenResponseExplainsMissingOfflineAccess(t *testing.T) {
	_, err := normalizeFeishuTokenResponse(feishuTokenResponse{
		Code:        0,
		AccessToken: "u-user-token",
		ExpiresIn:   7200,
	})
	if err == nil || !strings.Contains(err.Error(), "offline_access") {
		t.Fatalf("err=%v", err)
	}
}

func TestParseFeishuDocumentURL(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantToken string
		wantType  string
		wantErr   bool
	}{
		{
			name:      "Feishu docx",
			raw:       "https://foodtalks.feishu.cn/docx/HosadQWiIoiX4CxG3g7cPXaUnwc?from=navigation",
			wantToken: "HosadQWiIoiX4CxG3g7cPXaUnwc",
			wantType:  "docx",
		},
		{
			name:      "Lark wiki",
			raw:       "https://bytedance.larkoffice.com/wiki/HZQswdqYJirjPskb1wCczyb0nHb",
			wantToken: "HZQswdqYJirjPskb1wCczyb0nHb",
			wantType:  "wiki",
		},
		{
			name:      "legacy Lark doc",
			raw:       "https://workspace.larksuite.com/docs/doccn_abcdef123456",
			wantToken: "doccn_abcdef123456",
			wantType:  "doc",
		},
		{
			name:    "lookalike host",
			raw:     "https://foodtalks.feishu.cn.evil.test/docx/HosadQWiIoiX4CxG3g7cPXaUnwc",
			wantErr: true,
		},
		{
			name:    "insecure scheme",
			raw:     "http://foodtalks.feishu.cn/docx/HosadQWiIoiX4CxG3g7cPXaUnwc",
			wantErr: true,
		},
		{
			name:    "unsupported kind",
			raw:     "https://foodtalks.feishu.cn/base/HosadQWiIoiX4CxG3g7cPXaUnwc",
			wantErr: true,
		},
		{
			name:    "extra path",
			raw:     "https://foodtalks.feishu.cn/docx/HosadQWiIoiX4CxG3g7cPXaUnwc/edit",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := parseFeishuDocumentURL(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseFeishuDocumentURL(%q)=%+v, want error", tt.raw, ref)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFeishuDocumentURL(%q): %v", tt.raw, err)
			}
			if ref.Token != tt.wantToken || ref.Type != tt.wantType || ref.URL != tt.raw {
				t.Fatalf("parseFeishuDocumentURL(%q)=%+v", tt.raw, ref)
			}
		})
	}
}

func TestValidateFeishuDocumentRefCanResolveAndCrossCheckURL(t *testing.T) {
	raw := "https://foodtalks.feishu.cn/docx/HosadQWiIoiX4CxG3g7cPXaUnwc?from=navigation"
	ref, err := validateFeishuDocumentRef(feishuDocumentRef{URL: raw})
	if err != nil {
		t.Fatalf("validate URL-only ref: %v", err)
	}
	if ref.Token != "HosadQWiIoiX4CxG3g7cPXaUnwc" || ref.Type != "docx" || ref.URL != raw {
		t.Fatalf("URL-only ref=%+v", ref)
	}

	if _, err := validateFeishuDocumentRef(feishuDocumentRef{
		Token: "anotherToken123",
		Type:  "docx",
		URL:   raw,
	}); err == nil {
		t.Fatal("mismatched token and URL must fail")
	}
	if _, err := validateFeishuDocumentRef(feishuDocumentRef{
		Token: "HosadQWiIoiX4CxG3g7cPXaUnwc",
		Type:  "wiki",
		URL:   raw,
	}); err == nil {
		t.Fatal("mismatched type and URL must fail")
	}
}

func TestFeishuExportPollDelayStartsFastThenBacksOff(t *testing.T) {
	base := 250 * time.Millisecond
	want := []time.Duration{
		0,
		250 * time.Millisecond,
		250 * time.Millisecond,
		500 * time.Millisecond,
		500 * time.Millisecond,
		time.Second,
	}
	for attempt, expected := range want {
		if got := feishuExportPollDelay(base, attempt); got != expected {
			t.Fatalf("attempt %d delay=%s want=%s", attempt, got, expected)
		}
	}
	if got := feishuExportPollDelay(0, 3); got != 0 {
		t.Fatalf("zero base delay=%s want=0", got)
	}
	if got := feishuExportPollDelay(time.Second, 3); got != time.Second {
		t.Fatalf("delay cap=%s want=1s", got)
	}
}

func TestHTTPFeishuProviderSearchUsesUserTokenAndDocumentFilter(t *testing.T) {
	var gotAuthorization string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/suite/docs-api/search/object" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		gotAuthorization = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 0,
			"msg":  "Success",
			"data": map[string]any{
				"docs_entities": []map[string]any{{
					"docs_token": "doccn_test",
					"docs_type":  "doc",
					"title":      "新品趋势",
					"owner_id":   "ou_owner",
				}},
			},
		})
	}))
	defer server.Close()

	provider := newHTTPFeishuProvider(server.Client())
	provider.apiBaseURL = server.URL
	documents, err := provider.SearchDocuments(context.Background(), "u-user-token", "新品", 8)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if gotAuthorization != "Bearer u-user-token" {
		t.Fatalf("authorization=%q", gotAuthorization)
	}
	if gotBody["search_key"] != "新品" || gotBody["count"] != float64(8) {
		t.Fatalf("body=%+v", gotBody)
	}
	if !reflect.DeepEqual(gotBody["docs_types"], []any{"doc"}) {
		t.Fatalf("docs_types=%#v", gotBody["docs_types"])
	}
	if len(documents) != 1 || documents[0].Token != "doccn_test" || documents[0].Type != "doc" {
		t.Fatalf("documents=%+v", documents)
	}
}

func TestHTTPFeishuProviderExportsWikiNodeAsDocx(t *testing.T) {
	var createBody map[string]string
	var requestPaths []string
	docx := []byte{'P', 'K', 3, 4, 't', 'e', 's', 't'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPaths = append(requestPaths, r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer u-user-token" {
			t.Fatalf("missing user token on %s", r.URL.Path)
		}
		switch {
		case r.URL.Path == "/open-apis/wiki/v2/spaces/get_node":
			writeJSON(w, http.StatusOK, map[string]any{
				"code": 0,
				"data": map[string]any{"node": map[string]any{
					"obj_token": "doxcn_resolved",
					"obj_type":  "docx",
					"title":     "知识库文章",
				}},
			})
		case r.URL.Path == "/open-apis/drive/v1/export_tasks" && r.Method == http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Fatalf("decode create: %v", err)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"code": 0,
				"data": map[string]any{"ticket": "ticket-1"},
			})
		case r.URL.Path == "/open-apis/drive/v1/export_tasks/ticket-1":
			if r.URL.Query().Get("token") != "doxcn_resolved" {
				t.Fatalf("status token=%q", r.URL.Query().Get("token"))
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"code": 0,
				"data": map[string]any{"result": map[string]any{
					"file_name":     "知识库文章",
					"file_token":    "boxcn_export",
					"job_error_msg": "success",
					"job_status":    0,
				}},
			})
		case r.URL.Path == "/open-apis/drive/v1/export_tasks/file/boxcn_export/download":
			w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(docx)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer server.Close()

	provider := newHTTPFeishuProvider(server.Client())
	provider.apiBaseURL = server.URL
	exported, err := provider.ExportDocument(context.Background(), "u-user-token", feishuDocumentRef{
		Token: "wikcn_test",
		Type:  "wiki",
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if createBody["token"] != "doxcn_resolved" || createBody["type"] != "docx" || createBody["file_extension"] != "docx" {
		t.Fatalf("create body=%+v", createBody)
	}
	if exported.Filename != "知识库文章.docx" || string(exported.Content) != string(docx) {
		t.Fatalf("exported=%+v", exported)
	}
	if len(requestPaths) != 4 {
		t.Fatalf("request paths=%v", requestPaths)
	}
}

func TestHTTPFeishuProviderRejectsNonDocxDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/drive/v1/export_tasks" && r.Method == http.MethodPost:
			_, _ = io.WriteString(w, `{"code":0,"data":{"ticket":"ticket-1"}}`)
		case strings.Contains(r.URL.Path, "/export_tasks/ticket-1"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"result":{"file_token":"boxcn_export","job_status":0}}}`)
		default:
			_, _ = io.WriteString(w, `<html>not a docx</html>`)
		}
	}))
	defer server.Close()
	provider := newHTTPFeishuProvider(server.Client())
	provider.apiBaseURL = server.URL
	_, err := provider.ExportDocument(context.Background(), "u-user-token", feishuDocumentRef{
		Token: "doccn_test",
		Type:  "doc",
	})
	if err == nil || !strings.Contains(err.Error(), "did not return a DOCX") {
		t.Fatalf("err=%v", err)
	}
}

func TestHTTPFeishuProviderRetriesTransientStatusFailure(t *testing.T) {
	statusCalls := 0
	docx := []byte{'P', 'K', 3, 4, 'o', 'k'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/drive/v1/export_tasks" && r.Method == http.MethodPost:
			_, _ = io.WriteString(w, `{"code":0,"data":{"ticket":"ticket-1"}}`)
		case strings.Contains(r.URL.Path, "/export_tasks/ticket-1"):
			statusCalls++
			if statusCalls == 1 {
				http.Error(w, "temporary", http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"result":{"file_token":"boxcn_export","job_status":0}}}`)
		default:
			_, _ = w.Write(docx)
		}
	}))
	defer server.Close()

	provider := newHTTPFeishuProvider(server.Client())
	provider.apiBaseURL = server.URL
	provider.exportPollInterval = 0
	exported, err := provider.ExportDocument(context.Background(), "u-user-token", feishuDocumentRef{
		Token: "doccn_test",
		Type:  "doc",
	})
	if err != nil {
		t.Fatalf("export after transient poll failure: %v", err)
	}
	if statusCalls != 2 || !reflect.DeepEqual(exported.Content, docx) {
		t.Fatalf("statusCalls=%d exported=%+v", statusCalls, exported)
	}
}

func TestHTTPFeishuProviderSurfacesTerminalJobStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/drive/v1/export_tasks" && r.Method == http.MethodPost:
			_, _ = io.WriteString(w, `{"code":0,"data":{"ticket":"ticket-1"}}`)
		default:
			_, _ = io.WriteString(w, `{"code":0,"data":{"result":{"job_status":110}}}`)
		}
	}))
	defer server.Close()

	provider := newHTTPFeishuProvider(server.Client())
	provider.apiBaseURL = server.URL
	provider.exportPollInterval = 0
	_, err := provider.ExportDocument(context.Background(), "u-user-token", feishuDocumentRef{
		Token: "doccn_test",
		Type:  "doc",
	})
	var taskErr *feishuExportTaskError
	if !errors.As(err, &taskErr) || taskErr.JobStatus != 110 {
		t.Fatalf("err=%v, want feishuExportTaskError status 110", err)
	}
}

func TestHTTPFeishuProviderStopsAfterBoundedStatusFailures(t *testing.T) {
	statusCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/drive/v1/export_tasks" && r.Method == http.MethodPost:
			_, _ = io.WriteString(w, `{"code":0,"data":{"ticket":"ticket-1"}}`)
		default:
			statusCalls++
			http.Error(w, "temporary", http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()

	provider := newHTTPFeishuProvider(server.Client())
	provider.apiBaseURL = server.URL
	provider.exportPollInterval = 0
	provider.exportMaxPollErrors = 2
	_, err := provider.ExportDocument(context.Background(), "u-user-token", feishuDocumentRef{
		Token: "doccn_test",
		Type:  "doc",
	})
	if err == nil || !strings.Contains(err.Error(), "status unavailable") {
		t.Fatalf("err=%v, want bounded status failure", err)
	}
	if statusCalls != 2 {
		t.Fatalf("status calls=%d, want 2", statusCalls)
	}
}
