package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
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
	mux.Handle("/", fs)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("FBIF server listening on %s (API + static from %s)", addr, publicDir)
	if err := http.ListenAndServe(addr, withCORS(mux)); err != nil {
		log.Fatal(err)
	}
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

	return strings.Join(cleaned, "\n"), title, nil
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
