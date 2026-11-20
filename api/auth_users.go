package main

// auth_users.go — 排版工具自己的用户库（auth-users.json）：只记「这个人是谁、什么角色、停没停用」。
// 登录归入口和 FBIF 登录中心（本项目不存会话），飞书授权归授权模板（feishu-grants.json），这里都不存。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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

// authUser 的 ID 定下就不改：2026-11-10 换统一登录前用飞书登录过的人是 feishu:<open_id>，
// 之后新来的是统一登录的「企业编号:sub」，账号密码登录的是 account:<账号表 record_id>。
type authUser struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	AvatarURL string   `json:"avatar_url,omitempty"`
	Email     string   `json:"email,omitempty"`
	AuthMode  authMode `json:"auth_mode"`
	// CompanyCode 是统一登录给的企业编号（FBIF 是 FJY632B9P3E），账号密码登录是 account；换统一登录前的老记录是空的（都是 FBIF）。
	CompanyCode string   `json:"company_code,omitempty"`
	Role        authRole `json:"role,omitempty"`
	Status      string   `json:"status"`
	// FeishuOpenID 是这个人在本项目飞书应用下的 open_id：超级管理员名单按它认，授权飞书时补上。
	FeishuOpenID  string    `json:"feishu_open_id,omitempty"`
	FeishuUnionID string    `json:"feishu_union_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// LastLoginAt 是最近一次经入口来过（每小时最多记一次）；智能体按它判断这个人还在不在（auth_login.go agentCheck）。
	LastLoginAt time.Time `json:"last_login_at,omitempty"`
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
	user.CompanyCode = strings.TrimSpace(user.CompanyCode)
	user.FeishuOpenID = strings.TrimSpace(user.FeishuOpenID)
	user.FeishuUnionID = strings.TrimSpace(user.FeishuUnionID)
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
		if user.FeishuOpenID == "" {
			user.FeishuOpenID = existing.FeishuOpenID
		}
		if user.FeishuUnionID == "" {
			user.FeishuUnionID = existing.FeishuUnionID
		}
		if rawRole == "" {
			// 登录只带身份资料，不应把已授权的管理员降级成普通用户。
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

// visitRecordEvery：同一个人资料没变时，最近一次来过（last_login_at）最多这么久写一次盘。
const visitRecordEvery = time.Hour

// RecordVisit 把入口给的人对上用户库，返回库里的那一条（每个带人的请求都调）：
// 飞书登录的人先按 union_id 认（换统一登录前的老用户 ID 是 feishu:<open_id>，ID、角色、停用都跟着走），
// 认不出再按 ID 认，都没有就新建。资料（名字、头像、邮箱）以这次为准，空的不覆盖；
// LastLoginAt 记「最近一次经入口来过」，资料没变、一小时内记过就不写盘。
func (s *authUserStore) RecordVisit(visit authUser, now time.Time) (authUser, error) {
	if strings.TrimSpace(visit.ID) == "" {
		return authUser{}, errors.New("auth user id is required")
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return authUser{}, err
	}
	match := -1
	if union := strings.TrimSpace(visit.FeishuUnionID); union != "" && visit.AuthMode == authModeFeishu {
		for idx := range doc.Users {
			if doc.Users[idx].AuthMode == authModeFeishu && doc.Users[idx].FeishuUnionID == union {
				match = idx
				break
			}
		}
	}
	if match < 0 {
		for idx := range doc.Users {
			if doc.Users[idx].ID == visit.ID {
				match = idx
				break
			}
		}
	}
	if match < 0 {
		visit.LastLoginAt = now
		visit = normalizeAuthUser(visit, nil, now)
		doc.Users = append(doc.Users, visit)
		return visit, s.saveLocked(doc)
	}
	user := doc.Users[match]
	changed := user.AuthMode != visit.AuthMode
	for _, field := range []struct {
		dst *string
		src string
	}{
		{&user.Name, visit.Name}, {&user.AvatarURL, visit.AvatarURL}, {&user.Email, visit.Email},
		{&user.CompanyCode, visit.CompanyCode}, {&user.FeishuUnionID, visit.FeishuUnionID},
	} {
		if v := strings.TrimSpace(field.src); v != "" && v != *field.dst {
			*field.dst = v
			changed = true
		}
	}
	if !changed && !user.LastLoginAt.IsZero() && now.Sub(user.LastLoginAt) < visitRecordEvery {
		return user, nil
	}
	user.AuthMode = visit.AuthMode
	user.LastLoginAt = now
	user.UpdatedAt = now
	doc.Users[match] = user
	return user, s.saveLocked(doc)
}

// SetFeishuOpenID 记下这个人授权飞书时的 open_id（超级管理员名单按它认）。已经是这个值就不写盘。
func (s *authUserStore) SetFeishuOpenID(id, openID string, now time.Time) error {
	openID = strings.TrimSpace(openID)
	if openID == "" {
		return nil
	}
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
		if doc.Users[idx].FeishuOpenID == openID {
			return nil
		}
		doc.Users[idx].FeishuOpenID = openID
		doc.Users[idx].UpdatedAt = now.UTC()
		return s.saveLocked(doc)
	}
	return errors.New("auth user not found")
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
