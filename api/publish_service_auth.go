package main

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
)

const (
	publishSyncServiceTokenEnv = "PUBLISH_SYNC_SERVICE_TOKEN"
	publishSyncServiceHeader   = "X-Publish-Sync-Token"
	publishHTTPReviewEnv       = "PUBLISH_HTTP_REVIEW_ENABLED"
	publishHTTPReviewerEnv     = "PUBLISH_HTTP_REVIEWER_OPEN_ID"
)

var (
	publishClassifierVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	publishUnsafeHTMLTagPattern     = regexp.MustCompile(`(?i)<\s*(script|style|iframe|object|embed|form|link|meta|base|svg|math|input|button|textarea|select)\b`)
	publishUnsafeHTMLAttrPattern    = regexp.MustCompile(`(?i)\son[a-z0-9_-]*\s*=`)
	publishUnsafeHTMLURLPattern     = regexp.MustCompile(`(?i)\b(?:href|src)\s*=\s*['"]?\s*(?:javascript|vbscript):`)
)

func publishSyncServiceToken() string {
	return strings.TrimSpace(os.Getenv(publishSyncServiceTokenEnv))
}

func publishSyncServiceTokenConfigured() bool {
	return publishSyncServiceToken() != ""
}

func validPublishSyncServiceToken(input string) bool {
	got := strings.TrimSpace(input)
	want := publishSyncServiceToken()
	return want != "" && len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func requirePublishServiceToken(w http.ResponseWriter, r *http.Request) bool {
	if !publishSyncServiceTokenConfigured() {
		writeError(w, http.StatusServiceUnavailable, publishSyncServiceTokenEnv+" is not configured")
		return false
	}
	if !validPublishSyncServiceToken(r.Header.Get(publishSyncServiceHeader)) {
		writeError(w, http.StatusUnauthorized, "publish sync service token required")
		return false
	}
	return true
}

// requirePublishSubmitOrAdmin 给机器调用方最小的 submit/status 权限；管理密码仍可
// 供人工后台使用，但机器凭证不能借此进入 review/retry/reconcile/rules 管理面。
func requirePublishSubmitOrAdmin(w http.ResponseWriter, r *http.Request) bool {
	if got := r.Header.Get(publishSyncServiceHeader); strings.TrimSpace(got) != "" {
		if !publishSyncServiceTokenConfigured() {
			writeError(w, http.StatusServiceUnavailable, publishSyncServiceTokenEnv+" is not configured")
			return false
		}
		if !validPublishSyncServiceToken(got) {
			writeError(w, http.StatusUnauthorized, "publish sync service token required")
			return false
		}
		return true
	}
	return requireAdminPassword(w, r)
}

func publishHTTPReviewEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(publishHTTPReviewEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func publishHTTPReviewerOpenID() string {
	return strings.TrimSpace(os.Getenv(publishHTTPReviewerEnv))
}

func validatePublishMachineEvidence(contentKind, classification, classifierVersion, contentHTML string) error {
	if contentKind != "article" || classification != "ordinary_confirmed" {
		return fmt.Errorf("machine sync requires positive ordinary article classification evidence")
	}
	classifierVersion = strings.TrimSpace(classifierVersion)
	if !publishClassifierVersionPattern.MatchString(classifierVersion) {
		return fmt.Errorf("machine sync requires a valid classifier version")
	}
	if contentHTML != "" && (publishUnsafeHTMLTagPattern.MatchString(contentHTML) ||
		publishUnsafeHTMLAttrPattern.MatchString(contentHTML) || publishUnsafeHTMLURLPattern.MatchString(contentHTML)) {
		return fmt.Errorf("machine sync content contains unsafe active HTML")
	}
	// URL-only machine submissions are the primary monitoring contract: the
	// monitor supplies positive type evidence, while the layout service fetches
	// the article from the original URL itself.  A body is still validated when
	// a supplier intentionally sends content_html.
	if strings.TrimSpace(contentHTML) == "" {
		return nil
	}
	if len([]rune(strings.TrimSpace(htmlToPlainText(contentHTML)))) < 20 {
		return fmt.Errorf("machine sync requires a non-empty ordinary article body")
	}
	return nil
}
