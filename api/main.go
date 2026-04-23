package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var db *sql.DB

func main() {
	port := flag.Int("port", 9000, "API server port")
	dbPath := flag.String("db", "config.db", "SQLite database path")
	flag.Parse()

	var err error
	db, err = sql.Open("sqlite", *dbPath+"?_journal_mode=WAL")
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	if err := initDB(); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	mux := http.NewServeMux()

	// Config profiles
	mux.HandleFunc("GET /api/config/profiles", handleListProfiles)
	mux.HandleFunc("POST /api/config/profiles", handleCreateProfile)
	mux.HandleFunc("GET /api/config/profiles/{id}", handleGetProfile)
	mux.HandleFunc("PUT /api/config/profiles/{id}", handleUpdateProfile)
	mux.HandleFunc("DELETE /api/config/profiles/{id}", handleDeleteProfile)

	// Active config (what the frontend loads on startup)
	mux.HandleFunc("GET /api/config/active", handleGetActiveConfig)
	mux.HandleFunc("PUT /api/config/active", handleSetActiveConfig)

	// Config versions (history for a profile)
	mux.HandleFunc("GET /api/config/profiles/{id}/versions", handleListVersions)

	// Image upload to OSS (base64 from DOCX)
	mux.HandleFunc("POST /api/oss-upload", handleImageUpload)
	mux.HandleFunc("POST /api/wechat-upload", handleImageUpload) // legacy alias

	// Article fetch (URL repost via x-reader)
	mux.HandleFunc("POST /api/fetch-article", handleFetchArticle)

	// Article meta fetch (lightweight: title + cover image for "更多文章" cards)
	mux.HandleFunc("POST /api/fetch-article-meta", handleFetchArticleMeta)

	// Image proxy (bypass CORS for Canvas compositing)
	mux.HandleFunc("GET /api/image-proxy", handleImageProxy)

	// Legacy .doc (OLE2) → stub .docx conversion via LibreOffice + image cache
	mux.HandleFunc("POST /api/doc-to-docx", handleDocToDocx)

	// Cached image payload for images stripped from converted docx
	mux.HandleFunc("GET /api/doc-cache/{hash}/{filename}", handleDocImageCache)

	// Kick off the in-memory image cache GC loop
	startDocImageCacheGC()

	// Health check
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Serve static files from ../public
	publicDir := "../public"
	if _, err := os.Stat(publicDir); err != nil {
		publicDir = "public"
	}
	fs := http.FileServer(http.Dir(publicDir))
	// Force browsers to revalidate HTML + all JS/CSS on every load so a deploy
	// picks up the latest code immediately. Top-level modules are pinned with
	// ?v=<commit>, but nested imports (e.g. parser.js → punctuation.js) are
	// bare paths — without a server-side no-cache, an updated inner module
	// keeps serving from the browser's heuristic cache for hours.
	mux.Handle("/", noCacheStatic(fs))

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("FBIF server listening on %s (API + static from %s)", addr, publicDir)
	if err := http.ListenAndServe(addr, withCORS(mux)); err != nil {
		log.Fatal(err)
	}
}

// noCacheStatic forces revalidation of HTML / JS / CSS on every load.
// Top-level HTML and hashed modules are re-fetched so a deploy takes effect
// immediately; bare-path nested imports (e.g. parser.js → punctuation.js)
// also stay fresh without relying on query-string pins the author may forget
// to add. "no-cache" still allows 304s via If-Modified-Since, so repeat loads
// are cheap when nothing changed.
func noCacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" || strings.HasSuffix(p, ".html") ||
			strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".mjs") ||
			strings.HasSuffix(p, ".css") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func initDB() error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS profiles (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			name       TEXT    NOT NULL UNIQUE,
			is_default INTEGER NOT NULL DEFAULT 0,
			created_at TEXT    NOT NULL,
			updated_at TEXT    NOT NULL
		);
		CREATE TABLE IF NOT EXISTS config_versions (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
			version    INTEGER NOT NULL,
			config     TEXT    NOT NULL,
			created_at TEXT    NOT NULL,
			UNIQUE(profile_id, version)
		);
		CREATE TABLE IF NOT EXISTS active_config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`)
	if err != nil {
		return err
	}

	// Ensure a default profile exists
	var count int
	db.QueryRow("SELECT COUNT(*) FROM profiles").Scan(&count)
	if count == 0 {
		now := time.Now().UTC().Format(time.RFC3339)
		defaultConfig := ConfigData{
			FooterEnabled: true,
			FontFamily:    "mp-quote, 'PingFang SC', system-ui, -apple-system, BlinkMacSystemFont, 'Helvetica Neue', 'Hiragino Sans GB', 'Microsoft YaHei UI', 'Microsoft YaHei', Arial, sans-serif",
			FontSize:      "15px",
			HeadingSize:   "18px",
			TextColor:     "rgb(84, 69, 69)",
			LinkColor:     "rgb(0, 112, 192)",
			LineHeight:    "1.75em",
			LetterSpacing: "0.034em",
		}
		configJSON, _ := json.Marshal(defaultConfig)
		res, err := db.Exec("INSERT INTO profiles (name, is_default, created_at, updated_at) VALUES (?, 1, ?, ?)",
			"默认配置", now, now)
		if err != nil {
			return err
		}
		pid, _ := res.LastInsertId()
		db.Exec("INSERT INTO config_versions (profile_id, version, config, created_at) VALUES (?, 1, ?, ?)",
			pid, string(configJSON), now)
		db.Exec("INSERT OR REPLACE INTO active_config (key, value) VALUES ('profile_id', ?)", fmt.Sprintf("%d", pid))
	}
	return nil
}

// ---- Data Types ----

type ConfigData struct {
	FooterEnabled bool   `json:"footer_enabled"`
	FontFamily    string `json:"font_family"`
	FontSize      string `json:"font_size"`
	HeadingSize   string `json:"heading_size"`
	TextColor     string `json:"text_color"`
	LinkColor     string `json:"link_color"`
	LineHeight    string `json:"line_height"`
	LetterSpacing string `json:"letter_spacing"`
}

type Profile struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	Config    *ConfigData `json:"config,omitempty"`
	Version   int    `json:"version,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type ConfigVersion struct {
	ID        int64       `json:"id"`
	Version   int         `json:"version"`
	Config    *ConfigData `json:"config"`
	CreatedAt string      `json:"created_at"`
}

// ---- Helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func getLatestVersion(profileID int64) (*ConfigVersion, error) {
	row := db.QueryRow(
		"SELECT id, version, config, created_at FROM config_versions WHERE profile_id = ? ORDER BY version DESC LIMIT 1",
		profileID)
	var cv ConfigVersion
	var configStr string
	if err := row.Scan(&cv.ID, &cv.Version, &configStr, &cv.CreatedAt); err != nil {
		return nil, err
	}
	cv.Config = &ConfigData{}
	json.Unmarshal([]byte(configStr), cv.Config)
	return &cv, nil
}

// ---- Handlers ----

func handleListProfiles(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id, name, is_default, created_at, updated_at FROM profiles ORDER BY is_default DESC, id")
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	profiles := []Profile{}
	for rows.Next() {
		var p Profile
		rows.Scan(&p.ID, &p.Name, &p.IsDefault, &p.CreatedAt, &p.UpdatedAt)
		if v, err := getLatestVersion(p.ID); err == nil {
			p.Config = v.Config
			p.Version = v.Version
		}
		profiles = append(profiles, p)
	}
	writeJSON(w, 200, profiles)
}

func handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string      `json:"name"`
		Config *ConfigData `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, 400, "name is required")
		return
	}
	if req.Config == nil {
		req.Config = &ConfigData{FooterEnabled: true, FontSize: "15px", HeadingSize: "18px", TextColor: "rgb(84, 69, 69)", LinkColor: "rgb(0, 112, 192)", LineHeight: "1.75em"}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec("INSERT INTO profiles (name, is_default, created_at, updated_at) VALUES (?, 0, ?, ?)", req.Name, now, now)
	if err != nil {
		writeError(w, 409, "profile name already exists")
		return
	}
	pid, _ := res.LastInsertId()
	configJSON, _ := json.Marshal(req.Config)
	db.Exec("INSERT INTO config_versions (profile_id, version, config, created_at) VALUES (?, 1, ?, ?)", pid, string(configJSON), now)

	writeJSON(w, 201, Profile{ID: pid, Name: req.Name, Config: req.Config, Version: 1, CreatedAt: now, UpdatedAt: now})
}

func handleGetProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var p Profile
	err := db.QueryRow("SELECT id, name, is_default, created_at, updated_at FROM profiles WHERE id = ?", id).
		Scan(&p.ID, &p.Name, &p.IsDefault, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		writeError(w, 404, "profile not found")
		return
	}
	if v, err := getLatestVersion(p.ID); err == nil {
		p.Config = v.Config
		p.Version = v.Version
	}
	writeJSON(w, 200, p)
}

func handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name   string      `json:"name,omitempty"`
		Config *ConfigData `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	var profileID int64
	err := db.QueryRow("SELECT id FROM profiles WHERE id = ?", id).Scan(&profileID)
	if err != nil {
		writeError(w, 404, "profile not found")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	if req.Name != "" {
		if _, err := db.Exec("UPDATE profiles SET name = ?, updated_at = ? WHERE id = ?", req.Name, now, id); err != nil {
			writeError(w, 409, "profile name already exists")
			return
		}
	}

	if req.Config != nil {
		// Get current max version
		var maxVersion int
		db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM config_versions WHERE profile_id = ?", id).Scan(&maxVersion)
		newVersion := maxVersion + 1
		configJSON, _ := json.Marshal(req.Config)
		db.Exec("INSERT INTO config_versions (profile_id, version, config, created_at) VALUES (?, ?, ?, ?)",
			id, newVersion, string(configJSON), now)
		db.Exec("UPDATE profiles SET updated_at = ? WHERE id = ?", now, id)
	}

	// Return updated profile
	var p Profile
	db.QueryRow("SELECT id, name, is_default, created_at, updated_at FROM profiles WHERE id = ?", id).
		Scan(&p.ID, &p.Name, &p.IsDefault, &p.CreatedAt, &p.UpdatedAt)
	if v, err := getLatestVersion(p.ID); err == nil {
		p.Config = v.Config
		p.Version = v.Version
	}
	writeJSON(w, 200, p)
}

func handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var isDefault int
	err := db.QueryRow("SELECT is_default FROM profiles WHERE id = ?", id).Scan(&isDefault)
	if err != nil {
		writeError(w, 404, "profile not found")
		return
	}
	if isDefault == 1 {
		writeError(w, 400, "cannot delete default profile")
		return
	}
	db.Exec("DELETE FROM config_versions WHERE profile_id = ?", id)
	db.Exec("DELETE FROM profiles WHERE id = ?", id)
	w.WriteHeader(204)
}

func handleGetActiveConfig(w http.ResponseWriter, r *http.Request) {
	var profileIDStr string
	err := db.QueryRow("SELECT value FROM active_config WHERE key = 'profile_id'").Scan(&profileIDStr)
	if err != nil {
		writeError(w, 404, "no active config")
		return
	}
	var p Profile
	err = db.QueryRow("SELECT id, name, is_default, created_at, updated_at FROM profiles WHERE id = ?", profileIDStr).
		Scan(&p.ID, &p.Name, &p.IsDefault, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		writeError(w, 404, "active profile not found")
		return
	}
	if v, err := getLatestVersion(p.ID); err == nil {
		p.Config = v.Config
		p.Version = v.Version
	}
	writeJSON(w, 200, p)
}

func handleSetActiveConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProfileID int64 `json:"profile_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProfileID == 0 {
		writeError(w, 400, "profile_id is required")
		return
	}
	var exists int
	db.QueryRow("SELECT COUNT(*) FROM profiles WHERE id = ?", req.ProfileID).Scan(&exists)
	if exists == 0 {
		writeError(w, 404, "profile not found")
		return
	}
	db.Exec("INSERT OR REPLACE INTO active_config (key, value) VALUES ('profile_id', ?)", fmt.Sprintf("%d", req.ProfileID))
	// Return the now-active profile
	handleGetActiveConfig(w, r)
}

func handleListVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rows, err := db.Query(
		"SELECT id, version, config, created_at FROM config_versions WHERE profile_id = ? ORDER BY version DESC",
		id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	versions := []ConfigVersion{}
	for rows.Next() {
		var cv ConfigVersion
		var configStr string
		rows.Scan(&cv.ID, &cv.Version, &configStr, &cv.CreatedAt)
		cv.Config = &ConfigData{}
		json.Unmarshal([]byte(configStr), cv.Config)
		versions = append(versions, cv)
	}
	writeJSON(w, 200, versions)
}

// ---- Image Upload (base64 from DOCX → OSS or legacy proxy) ----

func handleImageUpload(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 50<<20)) // 50MB limit
	if err != nil {
		writeError(w, 400, "failed to read body")
		return
	}

	// Proxy to upload endpoint if configured
	legacyURL := os.Getenv("WECHAT_UPLOAD_ENDPOINT")
	if legacyURL != "" {
		proxyReq, err := http.NewRequestWithContext(r.Context(), "POST", legacyURL, bytes.NewReader(body))
		if err != nil {
			writeError(w, 500, "proxy error: "+err.Error())
			return
		}
		proxyReq.Header.Set("Content-Type", r.Header.Get("Content-Type"))
		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil {
			writeError(w, 502, "upstream error: "+err.Error())
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	// No upload endpoint — return base64 as-is (preview works; WeChat re-hosts on paste)
	var req struct {
		Base64Images map[string]string `json:"base64_images"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}

	results := make(map[string]string)
	for key, val := range req.Base64Images {
		results[key] = val
	}
	writeJSON(w, 200, map[string]interface{}{"results": results})
}

// ---- Article Fetch (URL repost via x-reader) ----

func handleFetchArticle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeError(w, 400, "url is required")
		return
	}

	// Validate URL format
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		writeError(w, 400, "invalid URL: must start with http:// or https://")
		return
	}

	// Try x-reader first, fall back to simple fetch
	content, title, err := fetchWithXReader(req.URL)
	if err != nil {
		// Fall back: try direct HTTP fetch + simple HTML extraction
		content, title, err = fetchDirect(req.URL)
		if err != nil {
			writeError(w, 502, "failed to fetch article: "+err.Error())
			return
		}
	}

	writeJSON(w, 200, map[string]string{
		"title":   title,
		"content": content,
		"source":  req.URL,
	})
}

func fetchWithXReader(url string) (content, title string, err error) {
	// x-reader outputs markdown by default
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "x-reader", url, "--format", "markdown")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("x-reader failed: %s %v", stderr.String(), err)
	}

	output := stdout.String()
	// Extract title from first # heading and strip x-reader metadata lines
	var cleaned []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if title == "" && strings.HasPrefix(trimmed, "# ") {
			title = strings.TrimPrefix(trimmed, "# ")
		}
		// Skip x-reader metadata lines (Author: xxx, Published: xxx, Source: xxx)
		if strings.HasPrefix(trimmed, "Author:") || strings.HasPrefix(trimmed, "Published:") ||
			strings.HasPrefix(trimmed, "Source:") {
			continue
		}
		// Skip combined metadata like "Author: xxx | Published: xxx"
		if strings.Contains(trimmed, "Author:") && strings.Contains(trimmed, "Published:") {
			continue
		}
		cleaned = append(cleaned, line)
	}

	joined := strings.Join(cleaned, "\n")

	// x-reader returns HTTP 200 even when WeChat serves a CAPTCHA/env-exception
	// stub. Surface those as errors so handleFetchArticle falls back to the
	// direct HTTP + extractArticle path (which preserves bold/refs).
	if looksLikeWeChatBlock(joined) {
		return "", "", fmt.Errorf("x-reader returned wechat block page")
	}

	return joined, title, nil
}

// looksLikeWeChatBlock reports whether the x-reader output is a WeChat
// anti-scrape stub (CAPTCHA / 环境异常) rather than real article content.
// Heuristic: short output containing any of the known block markers.
func looksLikeWeChatBlock(s string) bool {
	if len(s) > 2000 {
		return false
	}
	markers := []string{
		"环境异常",
		"完成验证后即可继续访问",
		"requiring CAPTCHA",
		"CAPTCHA",
	}
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func fetchDirect(url string) (content, title string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return "", "", fmt.Errorf("read body: %w", err)
	}

	// Extract article content and convert to Markdown
	markdown, extractedTitle := extractArticle(string(bodyBytes))
	return markdown, extractedTitle, nil
}

// ---- Article Meta (lightweight: title + cover for "更多文章" cards) ----

var (
	reMsgCdnURL    = regexp.MustCompile(`var\s+msg_cdn_url\s*=\s*"([^"]+)"`)
	reMsgTitle     = regexp.MustCompile(`var\s+msg_title\s*=\s*"((?:[^"\\]|\\.)*)"`)
	reTitleTag     = regexp.MustCompile(`(?i)<title[^>]*>([^<]+)</title>`)
	reImgDataSrc   = regexp.MustCompile(`<img[^>]+data-src="(https?://[^"]+)"`)
	hostAllowlistCDN = []string{
		"mmbiz.qpic.cn",
		"mmbiz.qlogo.cn",
		"wx.qlogo.cn",
		"mp.weixin.qq.com",
	}
)

// handleFetchArticleMeta fetches a WeChat (or generic) article URL and returns
// just the title and cover image URL — cheap, bounded response, for the
// "更多文章" card editor in the sidebar.
func handleFetchArticleMeta(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeError(w, 400, "url is required")
		return
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		writeError(w, 400, "invalid URL: must start with http:// or https://")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", req.URL, nil)
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	httpReq.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		writeError(w, 502, "failed to fetch: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		writeError(w, 502, fmt.Sprintf("upstream HTTP %d", resp.StatusCode))
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024)) // 2MB cap
	if err != nil {
		writeError(w, 502, "read body: "+err.Error())
		return
	}
	raw := string(body)

	// Cover priority: msg_cdn_url → og:image → twitter:image → first data-src img
	cover := ""
	if m := reMsgCdnURL.FindStringSubmatch(raw); len(m) > 1 {
		cover = m[1]
	}
	if cover == "" {
		cover = extractMeta(raw, "og:image")
	}
	if cover == "" {
		cover = extractMeta(raw, "twitter:image")
	}
	if cover == "" {
		if m := reImgDataSrc.FindStringSubmatch(raw); len(m) > 1 {
			cover = m[1]
		}
	}

	// Title priority: msg_title → og:title → <title>
	title := ""
	if m := reMsgTitle.FindStringSubmatch(raw); len(m) > 1 {
		title = decodeJSString(m[1])
	}
	if title == "" {
		title = extractMeta(raw, "og:title")
	}
	if title == "" {
		if m := reTitleTag.FindStringSubmatch(raw); len(m) > 1 {
			title = strings.TrimSpace(m[1])
		}
	}
	title = strings.TrimSpace(title)

	writeJSON(w, 200, map[string]string{
		"title":      title,
		"cover_url":  cover,
		"source_url": req.URL,
	})
}

// decodeJSString handles simple backslash escapes found in WeChat's var msg_title
// (e.g. \", \\, \n, 中).
func decodeJSString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		nc := s[i+1]
		switch nc {
		case '"', '\\', '/':
			b.WriteByte(nc)
			i++
		case 'n':
			b.WriteByte('\n')
			i++
		case 't':
			b.WriteByte('\t')
			i++
		case 'r':
			b.WriteByte('\r')
			i++
		case 'u':
			if i+5 < len(s) {
				var code int
				fmt.Sscanf(s[i+2:i+6], "%x", &code)
				b.WriteRune(rune(code))
				i += 5
			} else {
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ---- Image Proxy (bypass CORS for Canvas compositing) ----

// handleImageProxy streams an allowlisted image URL so the frontend Canvas can
// read its pixels without tainting. Only WeChat CDN hosts are allowed so this
// cannot be abused as an open proxy.
func handleImageProxy(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("url")
	if target == "" {
		writeError(w, 400, "url query param required")
		return
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		writeError(w, 400, "invalid URL")
		return
	}
	hostOK := false
	for _, h := range hostAllowlistCDN {
		if u.Host == h || strings.HasSuffix(u.Host, "."+h) {
			hostOK = true
			break
		}
	}
	if !hostOK {
		writeError(w, 403, "host not allowed: "+u.Host)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://mp.weixin.qq.com/")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, 502, "fetch failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		writeError(w, 502, fmt.Sprintf("upstream HTTP %d", resp.StatusCode))
		return
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/jpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	io.Copy(w, io.LimitReader(resp.Body, 10*1024*1024)) // 10MB cap
}

func init() {
	// Ensure data directory exists for SQLite
	if err := os.MkdirAll("data", 0755); err != nil {
		log.Printf("Warning: could not create data directory: %v", err)
	}
}

// ---- Doc image cache (strip from .docx, serve on demand) ----

type docCacheEntry struct {
	files   map[string][]byte
	expires time.Time
}

var (
	docImageCacheMu sync.Mutex
	docImageCache   = map[string]docCacheEntry{}
)

const docCacheTTL = 15 * time.Minute

func docCachePut(hash string, files map[string][]byte) {
	docImageCacheMu.Lock()
	defer docImageCacheMu.Unlock()
	docImageCache[hash] = docCacheEntry{files: files, expires: time.Now().Add(docCacheTTL)}
}

func docCacheGet(hash, filename string) ([]byte, bool) {
	docImageCacheMu.Lock()
	defer docImageCacheMu.Unlock()
	e, ok := docImageCache[hash]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(docImageCache, hash)
		return nil, false
	}
	data, ok := e.files[filename]
	return data, ok
}

func startDocImageCacheGC() {
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			docImageCacheMu.Lock()
			now := time.Now()
			for k, e := range docImageCache {
				if now.After(e.expires) {
					delete(docImageCache, k)
				}
			}
			docImageCacheMu.Unlock()
		}
	}()
}

// stripImagesFromDocx opens the docx zip, extracts every word/media/* entry
// into the in-memory cache (keyed by a sha256-prefix hash of the original
// docx bytes), and rewrites the zip with those entries removed. It injects
// word/_fbif_imageUrls.json mapping filename → /api/doc-cache/<hash>/<filename>
// so the frontend parseDocx can wire <img src> without fetching bytes again.
func stripImagesFromDocx(docxBytes []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(docxBytes), int64(len(docxBytes)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	sum := sha256.Sum256(docxBytes)
	hash := hex.EncodeToString(sum[:])[:16]

	images := map[string][]byte{}
	imageUrls := map[string]string{}

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "word/media/") && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", f.Name, err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", f.Name, err)
			}
			base := strings.TrimPrefix(f.Name, "word/media/")
			images[base] = data
			imageUrls[base] = "/api/doc-cache/" + hash + "/" + base
			continue
		}
		// Copy every other entry verbatim
		hdr := f.FileHeader
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			return nil, fmt.Errorf("create %s: %w", f.Name, err)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", f.Name, err)
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("copy %s: %w", f.Name, err)
		}
	}

	if len(imageUrls) > 0 {
		urlsJSON, err := json.Marshal(imageUrls)
		if err != nil {
			return nil, fmt.Errorf("encode imageUrls: %w", err)
		}
		uw, err := zw.Create("word/_fbif_imageUrls.json")
		if err != nil {
			return nil, fmt.Errorf("create imageUrls file: %w", err)
		}
		if _, err := uw.Write(urlsJSON); err != nil {
			return nil, fmt.Errorf("write imageUrls: %w", err)
		}
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}

	if len(images) > 0 {
		docCachePut(hash, images)
	}
	return out.Bytes(), nil
}

func handleDocImageCache(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	filename := r.PathValue("filename")
	// Sanitize: filename must be plain (no slashes, no parent refs).
	if filename == "" || strings.ContainsAny(filename, "/\\") || strings.HasPrefix(filename, ".") {
		http.NotFound(w, r)
		return
	}
	data, ok := docCacheGet(hash, filename)
	if !ok {
		http.NotFound(w, r)
		return
	}
	mime := docImageMimeFromExt(strings.ToLower(path.Ext(filename)))
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=900")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}

func docImageMimeFromExt(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".svg":
		return "image/svg+xml"
	case ".emf":
		return "image/x-emf"
	case ".wmf":
		return "image/x-wmf"
	}
	return ""
}

// ---- Legacy .doc → .docx via CloudConvert ----

var ole2Magic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

const maxDocUploadBytes = 50 << 20 // 50 MB cap for /api/doc-to-docx

// sameOrigin enforces a CSRF-style check for endpoints that may spend
// paid backend quota (CloudConvert). A cross-site <form>/fetch from a
// third-party page always ships an Origin header; same-origin fetches
// from our own app match r.Host. curl / server-to-server traffic omits
// both headers and is allowed.
func sameOrigin(r *http.Request) bool {
	check := func(raw string) (matched, decided bool) {
		if raw == "" {
			return false, false
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return false, true
		}
		return u.Host == r.Host, true
	}
	if ok, decided := check(r.Header.Get("Origin")); decided {
		return ok
	}
	if ok, decided := check(r.Header.Get("Referer")); decided {
		return ok
	}
	return true
}

// handleDocToDocx accepts a multipart upload with field "file" containing an
// OLE2 (.doc) binary and returns real .docx bytes. Tries the self-hosted
// LibreOffice service first (fast, unlimited, private); falls back to
// CloudConvert if that's not configured or fails (network/5xx).
func handleDocToDocx(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, 403, "cross-origin request not allowed")
		return
	}
	// Hard cap the request body before ParseMultipartForm so Go doesn't
	// spill arbitrarily large uploads to temp files.
	r.Body = http.MaxBytesReader(w, r.Body, maxDocUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, 413, fmt.Sprintf("file too large (max %d MB)", maxDocUploadBytes>>20))
			return
		}
		writeError(w, 400, "failed to parse multipart: "+err.Error())
		return
	}
	upload, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "file field required")
		return
	}
	defer upload.Close()

	data, err := io.ReadAll(upload)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, 413, fmt.Sprintf("file too large (max %d MB)", maxDocUploadBytes>>20))
			return
		}
		writeError(w, 400, "read file: "+err.Error())
		return
	}
	if len(data) < 8 || !bytes.Equal(data[:8], ole2Magic) {
		writeError(w, 400, "not an OLE2 .doc file (missing magic bytes)")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	var docx []byte
	var usedPath string
	loURL := os.Getenv("LIBREOFFICE_URL")
	loToken := os.Getenv("LIBREOFFICE_TOKEN")
	// Token is optional: when LibreOffice is reached via localhost (same-box
	// deploy), no auth layer is needed. A public URL still needs one.
	if loURL != "" {
		docx, err = convertViaLibreOffice(ctx, loURL, loToken, data, header.Filename)
		if err == nil {
			usedPath = "libreoffice"
		} else {
			log.Printf("libreoffice failed (%v), falling back to cloudconvert", err)
		}
	}
	if docx == nil {
		apiKey := os.Getenv("CLOUDCONVERT_API_KEY")
		if apiKey == "" {
			writeError(w, 500, "no converter available: LIBREOFFICE_URL/TOKEN and CLOUDCONVERT_API_KEY both unset")
			return
		}
		docx, err = cloudConvertDocToDocx(ctx, apiKey, data, header.Filename)
		if err != nil {
			log.Printf("cloudconvert error: %v", err)
			writeError(w, 502, "conversion failed: "+err.Error())
			return
		}
		usedPath = "cloudconvert"
	}

	// Strip embedded images out of the docx and replace them with URL refs
	// served from an in-memory cache. This shaves the client download from
	// ~14 MB to ~200 KB for the stub, lets the browser render text before
	// images finish transferring, and preserves image bytes verbatim.
	if stub, stripErr := stripImagesFromDocx(docx); stripErr == nil {
		docx = stub
	} else {
		log.Printf("strip images failed (%v), returning full docx", stripErr)
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", `inline; filename="converted.docx"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Conversion-Path", usedPath)
	w.Write(docx)
}

// convertViaLibreOffice POSTs the .doc to the self-hosted unoserver HTTP API
// (reached via Caddy reverse proxy with Bearer auth) and returns .docx bytes.
func convertViaLibreOffice(ctx context.Context, baseURL, token string, docBytes []byte, filename string) ([]byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("convert-to", "docx"); err != nil {
		return nil, err
	}
	safe := filename
	if safe == "" {
		safe = "input.doc"
	}
	fw, err := mw.CreateFormFile("file", safe)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(docBytes); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(baseURL, "/")+"/request", &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("libreoffice status %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 100<<20))
}

// cloudConvertDocToDocx drives a CloudConvert job: create → upload → poll → download.
// Returns the converted .docx bytes.
func cloudConvertDocToDocx(ctx context.Context, apiKey string, docBytes []byte, filename string) ([]byte, error) {
	const base = "https://api.cloudconvert.com/v2"

	// 1) Create job
	jobBody := `{"tasks":{"import-1":{"operation":"import/upload"},"convert-1":{"operation":"convert","input":"import-1","input_format":"doc","output_format":"docx"},"export-1":{"operation":"export/url","input":"convert-1"}}}`
	req, _ := http.NewRequestWithContext(ctx, "POST", base+"/jobs", strings.NewReader(jobBody))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("create job status %d: %s", resp.StatusCode, string(body))
	}

	var jobResp struct {
		Data struct {
			ID    string `json:"id"`
			Tasks []struct {
				Name      string `json:"name"`
				Operation string `json:"operation"`
				Status    string `json:"status"`
				Result    struct {
					Form *struct {
						URL        string            `json:"url"`
						Parameters map[string]string `json:"parameters"`
					} `json:"form"`
				} `json:"result"`
			} `json:"tasks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &jobResp); err != nil {
		return nil, fmt.Errorf("decode job: %w", err)
	}
	jobID := jobResp.Data.ID
	var form *struct {
		URL        string            `json:"url"`
		Parameters map[string]string `json:"parameters"`
	}
	for _, t := range jobResp.Data.Tasks {
		if t.Operation == "import/upload" && t.Result.Form != nil {
			form = t.Result.Form
			break
		}
	}
	if form == nil {
		return nil, fmt.Errorf("no import/upload form in job response")
	}

	// 2) Upload file as multipart to CloudConvert's storage (order matters: file last)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range form.Parameters {
		if err := mw.WriteField(k, v); err != nil {
			return nil, fmt.Errorf("write field %s: %w", k, err)
		}
	}
	safeName := filename
	if safeName == "" {
		safeName = "input.doc"
	}
	fw, err := mw.CreateFormFile("file", safeName)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := fw.Write(docBytes); err != nil {
		return nil, fmt.Errorf("write file part: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart: %w", err)
	}

	upReq, _ := http.NewRequestWithContext(ctx, "POST", form.URL, &buf)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upResp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}
	upBody, _ := io.ReadAll(upResp.Body)
	upResp.Body.Close()
	if upResp.StatusCode >= 300 {
		return nil, fmt.Errorf("upload status %d: %s", upResp.StatusCode, string(upBody))
	}

	// 3) Poll job until finished, errored, or the request context expires.
	var exportURL string
pollLoop:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		pollReq, _ := http.NewRequestWithContext(ctx, "GET", base+"/jobs/"+jobID, nil)
		pollReq.Header.Set("Authorization", "Bearer "+apiKey)
		pollResp, err := http.DefaultClient.Do(pollReq)
		if err != nil {
			return nil, fmt.Errorf("poll: %w", err)
		}
		pollBody, _ := io.ReadAll(pollResp.Body)
		pollResp.Body.Close()
		if pollResp.StatusCode >= 300 {
			return nil, fmt.Errorf("poll status %d: %s", pollResp.StatusCode, string(pollBody))
		}

		var pr struct {
			Data struct {
				Status string `json:"status"`
				Tasks  []struct {
					Operation string `json:"operation"`
					Status    string `json:"status"`
					Message   string `json:"message"`
					Code      string `json:"code"`
					Result    struct {
						Files []struct {
							Filename string `json:"filename"`
							URL      string `json:"url"`
						} `json:"files"`
					} `json:"result"`
				} `json:"tasks"`
			} `json:"data"`
		}
		if err := json.Unmarshal(pollBody, &pr); err != nil {
			return nil, fmt.Errorf("decode poll: %w", err)
		}

		switch pr.Data.Status {
		case "finished":
			for _, t := range pr.Data.Tasks {
				if t.Operation == "export/url" && len(t.Result.Files) > 0 {
					exportURL = t.Result.Files[0].URL
					break
				}
			}
			if exportURL == "" {
				return nil, fmt.Errorf("job finished but no export URL")
			}
			break pollLoop
		case "error":
			msg := "job failed"
			for _, t := range pr.Data.Tasks {
				if t.Status == "error" && t.Message != "" {
					msg = t.Operation + ": " + t.Message
					break
				}
			}
			return nil, fmt.Errorf("%s", msg)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	if exportURL == "" {
		return nil, fmt.Errorf("timed out waiting for conversion")
	}

	// 4) Download converted .docx
	dlReq, _ := http.NewRequestWithContext(ctx, "GET", exportURL, nil)
	dlResp, err := http.DefaultClient.Do(dlReq)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode >= 300 {
		return nil, fmt.Errorf("download status %d", dlResp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(dlResp.Body, 100<<20))
}
