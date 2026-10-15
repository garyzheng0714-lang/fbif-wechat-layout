package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type authMode string

const (
	authModeFeishu   authMode = "feishu"
	authModePassword authMode = "password"
	authUserActive            = "active"
	authUserDisabled          = "disabled"
)

type authRole string

const (
	authRoleUser       authRole = "user"
	authRoleAdmin      authRole = "admin"
	authRoleSuperAdmin authRole = "super_admin"
)

type authUser struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	AvatarURL              string    `json:"avatar_url,omitempty"`
	Email                  string    `json:"email,omitempty"`
	Username               string    `json:"username,omitempty"`
	PasswordHash           string    `json:"password_hash,omitempty"`
	AuthMode               authMode  `json:"auth_mode"`
	Role                   authRole  `json:"role,omitempty"`
	Status                 string    `json:"status"`
	FeishuOpenID           string    `json:"feishu_open_id,omitempty"`
	FeishuUnionID          string    `json:"feishu_union_id,omitempty"`
	FeishuRefreshTokenSeal string    `json:"feishu_refresh_token_seal,omitempty"`
	SessionVersion         int64     `json:"session_version"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	LastLoginAt            time.Time `json:"last_login_at,omitempty"`
	// LastSeenAt 是最近一次打开页面（/api/me 续期会话）的时间，按小时节流写入。
	// 会话 30 天滑动续期，飞书授权保活只覆盖这 30 天内用过的人。
	LastSeenAt time.Time `json:"last_seen_at,omitempty"`
	// FeishuTokenRefreshedAt / FeishuRefreshExpiresAt：refresh_token 上次轮换
	// 时间与飞书给的到期时间。refresh_token 约 7 天、只能用一次，后台保活据此
	// 在过期前替用户轮换。
	FeishuTokenRefreshedAt time.Time `json:"feishu_token_refreshed_at,omitempty"`
	FeishuRefreshExpiresAt time.Time `json:"feishu_refresh_expires_at,omitempty"`
	// FeishuReauthRequired：飞书明确拒绝了 refresh_token（过期、撤销、满 365
	// 天等），只能让用户重新用飞书登录；登录成功即清除。
	FeishuReauthRequired bool `json:"feishu_reauth_required,omitempty"`
}

type authUserDocument struct {
	SchemaVersion int        `json:"schema_version"`
	Users         []authUser `json:"users"`
}

type authUserStore struct {
	mu   sync.RWMutex
	path string
}

func newAuthUserStore(path string) *authUserStore {
	return &authUserStore{path: strings.TrimSpace(path)}
}

func (s *authUserStore) loadLocked() (authUserDocument, error) {
	doc := authUserDocument{SchemaVersion: 1, Users: []authUser{}}
	if s.path == "" {
		return doc, nil
	}
	body, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return doc, fmt.Errorf("read auth users: %w", err)
	}
	if len(body) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return doc, fmt.Errorf("decode auth users: %w", err)
	}
	if doc.SchemaVersion != 1 {
		return doc, fmt.Errorf("unsupported auth user schema version %d", doc.SchemaVersion)
	}
	if doc.Users == nil {
		doc.Users = []authUser{}
	}
	return doc, nil
}

func (s *authUserStore) saveLocked(doc authUserDocument) error {
	if s.path == "" {
		return errors.New("auth user store path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create auth user directory: %w", err)
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode auth users: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".auth-users-*.tmp")
	if err != nil {
		return fmt.Errorf("create auth user temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod auth user temp file: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("write auth user temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync auth user temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close auth user temp file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace auth user store: %w", err)
	}
	return nil
}

func normalizeAuthUser(user authUser, existing *authUser, now time.Time) authUser {
	user.ID = strings.TrimSpace(user.ID)
	user.Name = strings.TrimSpace(user.Name)
	user.AvatarURL = strings.TrimSpace(user.AvatarURL)
	user.Email = strings.TrimSpace(user.Email)
	user.Username = strings.ToLower(strings.TrimSpace(user.Username))
	rawRole := strings.TrimSpace(string(user.Role))
	user.Role = normalizeAuthRole(user.Role)
	user.Status = strings.TrimSpace(user.Status)
	if user.Status == "" {
		user.Status = authUserActive
	}
	if existing != nil {
		if user.CreatedAt.IsZero() {
			user.CreatedAt = existing.CreatedAt
		}
		if user.SessionVersion == 0 {
			user.SessionVersion = existing.SessionVersion
		}
		if user.PasswordHash == "" {
			user.PasswordHash = existing.PasswordHash
		}
		if user.FeishuRefreshTokenSeal == "" {
			user.FeishuRefreshTokenSeal = existing.FeishuRefreshTokenSeal
			// 沿用旧令牌时，它的续期时间与失效标记也一并沿用。
			if user.FeishuTokenRefreshedAt.IsZero() {
				user.FeishuTokenRefreshedAt = existing.FeishuTokenRefreshedAt
			}
			if user.FeishuRefreshExpiresAt.IsZero() {
				user.FeishuRefreshExpiresAt = existing.FeishuRefreshExpiresAt
			}
			if !user.FeishuReauthRequired {
				user.FeishuReauthRequired = existing.FeishuReauthRequired
			}
		}
		if user.LastSeenAt.Before(existing.LastSeenAt) {
			user.LastSeenAt = existing.LastSeenAt
		}
		if rawRole == "" {
			// 登录回调只携带身份资料，不应把已授权的管理员降级成普通用户。
			// 显式的降级由权限管理接口完成，并传入新的角色。
			if existing.Role == authRoleAdmin || existing.Role == authRoleSuperAdmin {
				user.Role = existing.Role
			}
		}
	}
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	user.UpdatedAt = now
	return user
}

func normalizeAuthRole(role authRole) authRole {
	switch authRole(strings.TrimSpace(string(role))) {
	case authRoleAdmin:
		return authRoleAdmin
	case authRoleSuperAdmin:
		return authRoleSuperAdmin
	default:
		return authRoleUser
	}
}

func (s *authUserStore) Upsert(user authUser) error {
	if strings.TrimSpace(user.ID) == "" {
		return errors.New("auth user id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for idx := range doc.Users {
		if doc.Users[idx].ID == user.ID {
			user = normalizeAuthUser(user, &doc.Users[idx], now)
			doc.Users[idx] = user
			return s.saveLocked(doc)
		}
	}
	doc.Users = append(doc.Users, normalizeAuthUser(user, nil, now))
	return s.saveLocked(doc)
}

func (s *authUserStore) Get(id string) (authUser, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, err := s.loadLocked()
	if err != nil {
		return authUser{}, false, err
	}
	for _, user := range doc.Users {
		if user.ID == id {
			return user, true, nil
		}
	}
	return authUser{}, false, nil
}

func (s *authUserStore) List() ([]authUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	users := append([]authUser(nil), doc.Users...)
	return users, nil
}

func (s *authUserStore) UpdateRole(id string, role authRole, now time.Time) (authUser, error) {
	role = normalizeAuthRole(role)
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return authUser{}, err
	}
	for idx := range doc.Users {
		if doc.Users[idx].ID != id {
			continue
		}
		doc.Users[idx].Role = role
		doc.Users[idx].UpdatedAt = now.UTC()
		if err := s.saveLocked(doc); err != nil {
			return authUser{}, err
		}
		return doc.Users[idx], nil
	}
	return authUser{}, errors.New("auth user not found")
}

func (s *authUserStore) FindByUsername(username string) (authUser, bool, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, err := s.loadLocked()
	if err != nil {
		return authUser{}, false, err
	}
	for _, user := range doc.Users {
		if user.AuthMode == authModePassword && user.Username == username {
			return user, true, nil
		}
	}
	return authUser{}, false, nil
}

func (s *authUserStore) IncrementSessionVersion(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	for idx := range doc.Users {
		if doc.Users[idx].ID != id {
			continue
		}
		doc.Users[idx].SessionVersion++
		doc.Users[idx].UpdatedAt = now.UTC()
		return s.saveLocked(doc)
	}
	return errors.New("auth user not found")
}

func (s *authUserStore) UpdateFeishuTokens(id, refreshSeal string, lastLoginAt time.Time) (authUser, error) {
	return s.updateFeishuTokens(id, refreshSeal, lastLoginAt, time.Time{}, time.Time{})
}

// RotateFeishuRefreshToken 原子替换轮换后的 refresh_token，记下轮换时间与
// 飞书给的到期时间，并清除「需重新授权」标记。只在库里仍是这次刷新用掉的那份
// 令牌（usedSeal）时才写：刷新期间这个人重新登录过、或别的进程已换上更新的
// 授权，就不拿旧授权链的结果覆盖它，返回 false。
func (s *authUserStore) RotateFeishuRefreshToken(id, usedSeal, refreshSeal string, refreshedAt, expiresAt time.Time) (authUser, bool, error) {
	if refreshSeal == "" {
		return authUser{}, false, errors.New("refresh token seal is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return authUser{}, false, err
	}
	for idx := range doc.Users {
		if doc.Users[idx].ID != id {
			continue
		}
		if doc.Users[idx].FeishuRefreshTokenSeal != usedSeal {
			return doc.Users[idx], false, nil
		}
		doc.Users[idx].FeishuRefreshTokenSeal = refreshSeal
		doc.Users[idx].FeishuReauthRequired = false
		doc.Users[idx].FeishuTokenRefreshedAt = refreshedAt.UTC()
		doc.Users[idx].FeishuRefreshExpiresAt = expiresAt.UTC()
		doc.Users[idx].UpdatedAt = time.Now().UTC()
		if err := s.saveLocked(doc); err != nil {
			return authUser{}, false, err
		}
		return doc.Users[idx], true, nil
	}
	return authUser{}, false, errors.New("auth user not found")
}

func (s *authUserStore) updateFeishuTokens(id, refreshSeal string, lastLoginAt, refreshedAt, expiresAt time.Time) (authUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return authUser{}, err
	}
	for idx := range doc.Users {
		if doc.Users[idx].ID != id {
			continue
		}
		if refreshSeal != "" {
			doc.Users[idx].FeishuRefreshTokenSeal = refreshSeal
			doc.Users[idx].FeishuReauthRequired = false
		}
		if !refreshedAt.IsZero() {
			doc.Users[idx].FeishuTokenRefreshedAt = refreshedAt.UTC()
			doc.Users[idx].FeishuRefreshExpiresAt = expiresAt.UTC()
		}
		if !lastLoginAt.IsZero() {
			doc.Users[idx].LastLoginAt = lastLoginAt.UTC()
		}
		doc.Users[idx].UpdatedAt = time.Now().UTC()
		if err := s.saveLocked(doc); err != nil {
			return authUser{}, err
		}
		return doc.Users[idx], nil
	}
	return authUser{}, errors.New("auth user not found")
}

// MarkFeishuReauthRequired 在飞书明确拒绝 refresh_token 后清掉它并标记需重新
// 授权。只在库里仍是这次用掉的那份令牌时才生效：并发轮换（另一请求或进程
// 已换上新令牌）时返回 false，调用方应改用新令牌重试，不能误伤。
func (s *authUserStore) MarkFeishuReauthRequired(id, usedSeal string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return false, err
	}
	for idx := range doc.Users {
		if doc.Users[idx].ID != id {
			continue
		}
		if doc.Users[idx].FeishuRefreshTokenSeal != usedSeal {
			return false, nil
		}
		doc.Users[idx].FeishuRefreshTokenSeal = ""
		doc.Users[idx].FeishuReauthRequired = true
		doc.Users[idx].UpdatedAt = now.UTC()
		return true, s.saveLocked(doc)
	}
	return false, errors.New("auth user not found")
}

// TouchLastSeen 记录用户最近一次使用；距上次记录不足 minInterval 时不写盘。
func (s *authUserStore) TouchLastSeen(id string, now time.Time, minInterval time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	for idx := range doc.Users {
		if doc.Users[idx].ID != id {
			continue
		}
		if now.Sub(doc.Users[idx].LastSeenAt) < minInterval {
			return nil
		}
		doc.Users[idx].LastSeenAt = now.UTC()
		return s.saveLocked(doc)
	}
	return errors.New("auth user not found")
}

func hashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	body, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func verifyPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
