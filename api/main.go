package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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

	// Health check
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("FBIF API server listening on %s", addr)
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
		db.Exec("UPDATE profiles SET name = ?, updated_at = ? WHERE id = ?", req.Name, now, id)
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
	db.QueryRow("SELECT id, name, is_default, created_at, updated_at FROM profiles WHERE id = ?", profileIDStr).
		Scan(&p.ID, &p.Name, &p.IsDefault, &p.CreatedAt, &p.UpdatedAt)
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

func init() {
	// Ensure data directory exists for SQLite
	if err := os.MkdirAll("data", 0755); err != nil {
		log.Printf("Warning: could not create data directory: %v", err)
	}
}
