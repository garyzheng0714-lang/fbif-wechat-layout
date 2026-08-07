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
)

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

func TestHTTPFeishuProviderListsOnlyGroupChatsAcrossPages(t *testing.T) {
	var pageTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/im/v1/chats" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer user-chat-token" {
			t.Fatalf("authorization=%q", got)
		}
		if got := r.URL.Query().Get("user_id_type"); got != "open_id" {
			t.Fatalf("user_id_type=%q", got)
		}
		pageToken := r.URL.Query().Get("page_token")
		pageTokens = append(pageTokens, pageToken)
		if pageToken == "" {
			writeJSON(w, http.StatusOK, map[string]any{
				"code": 0,
				"data": map[string]any{
					"items": []map[string]any{
						{"chat_id": "oc_group_1", "name": "内容审核群", "chat_mode": "group"},
						{"chat_id": "ou_direct", "name": "单聊", "chat_mode": "p2p"},
					},
					"has_more":   true,
					"page_token": "next-page",
				},
			})
			return
		}
		if pageToken != "next-page" {
			t.Fatalf("unexpected page token=%q", pageToken)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 0,
			"data": map[string]any{
				"items": []map[string]any{
					{"chat_id": "oc_topic_2", "name": "专题审核群", "chat_mode": "topic", "external": true},
				},
				"has_more": false,
			},
		})
	}))
	defer server.Close()

	provider := newHTTPFeishuProvider(server.Client())
	provider.apiBaseURL = server.URL
	chats, err := provider.ListChats(context.Background(), "user-chat-token")
	if err != nil {
		t.Fatalf("list chats: %v", err)
	}
	if !reflect.DeepEqual(pageTokens, []string{"", "next-page"}) {
		t.Fatalf("page tokens=%v", pageTokens)
	}
	if len(chats) != 2 || chats[0].ChatID != "oc_group_1" || chats[1].ChatID != "oc_topic_2" || !chats[1].External {
		t.Fatalf("filtered chats=%+v", chats)
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

func TestHTTPFeishuProviderWikiResolveDeniedMapsToReloginHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/wiki/v2/spaces/get_node" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 99991679,
			"msg":  "Unauthorized",
		})
	}))
	defer server.Close()

	provider := newHTTPFeishuProvider(server.Client())
	provider.apiBaseURL = server.URL
	_, err := provider.ExportDocument(context.Background(), "u-user-token", feishuDocumentRef{
		Token: "wikcn_test",
		Type:  "wiki",
	})
	var wikiErr *feishuWikiAccessError
	if !errors.As(err, &wikiErr) {
		t.Fatalf("expected feishuWikiAccessError, got %v", err)
	}
	status, code, message := feishuExportErrorResponse(err)
	if status != http.StatusForbidden || code != "feishu_wiki_access_denied" {
		t.Fatalf("status=%d code=%q", status, code)
	}
	if !strings.Contains(message, "重新用飞书登录") {
		t.Fatalf("message=%q", message)
	}
}

func TestHTTPFeishuProviderWikiNonDocumentNodeIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/wiki/v2/spaces/get_node" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 0,
			"data": map[string]any{"node": map[string]any{
				"obj_token": "shtcn_sheet",
				"obj_type":  "sheet",
				"title":     "知识库表格",
			}},
		})
	}))
	defer server.Close()

	provider := newHTTPFeishuProvider(server.Client())
	provider.apiBaseURL = server.URL
	_, err := provider.ExportDocument(context.Background(), "u-user-token", feishuDocumentRef{
		Token: "wikcn_test",
		Type:  "wiki",
	})
	if !errors.Is(err, errFeishuWikiNotDocument) {
		t.Fatalf("expected errFeishuWikiNotDocument, got %v", err)
	}
	status, code, _ := feishuExportErrorResponse(err)
	if status != http.StatusBadRequest || code != "feishu_wiki_not_document" {
		t.Fatalf("status=%d code=%q", status, code)
	}
}
