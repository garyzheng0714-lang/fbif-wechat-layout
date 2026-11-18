package mcpauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// grant 一份授权：哪个人允许了哪个应用。令牌只存哈希。
type grant struct {
	ID         string    `json:"id"`
	PersonID   string    `json:"person_id"`
	PersonName string    `json:"person_name"`
	ClientID   string    `json:"client_id"`
	ClientName string    `json:"client_name"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`

	// RefreshHashes 现在认的刷新令牌：平常只有一个；同一台电脑上几个进程同时拿旧令牌刷新（refreshGrace 内），
	// 各自拿到的新令牌都认，最多留 maxRefreshTokens 个。
	RefreshHashes    []string  `json:"refresh_hashes"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	// UsedRefresh 用掉的刷新令牌：换新后 refreshGrace 内还认，过了再拿来用就是被盗用，整份吊销。
	UsedRefresh []usedRefresh `json:"used_refresh,omitempty"`

	Access []accessToken `json:"access"`
}

type usedRefresh struct {
	Hash string    `json:"hash"`
	At   time.Time `json:"at"`
}

type accessToken struct {
	Hash      string    `json:"hash"`
	ExpiresAt time.Time `json:"expires_at"`
}

type registeredClient struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	RedirectURIs []string  `json:"redirect_uris"`
	CreatedAt    time.Time `json:"created_at"`
}

type storeFile struct {
	Clients map[string]registeredClient `json:"clients"`
	Grants  map[string]*grant           `json:"grants"`
}

const (
	maxRegisteredClients = 5000
	maxRefreshTokens     = 4
	maxAccessTokens      = 8
	maxUsedRefresh       = 32
)

var errRefreshReused = errors.New("mcpauth: 刷新令牌被重复使用")

// store 单进程：全在内存里，每次改动整份写盘（先写临时文件再改名），写盘失败撤回改动。
type store struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	data    storeFile
	access  map[string]string // 访问令牌哈希 → 授权编号
	refresh map[string]string // 当前刷新令牌哈希 → 授权编号
	used    map[string]string // 用掉的刷新令牌哈希 → 授权编号
	saved   map[string]time.Time
}

func openStore(path string, now func() time.Time) (*store, error) {
	s := &store{path: path, now: now, saved: map[string]time.Time{}}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, fmt.Errorf("mcpauth: %s: %w", path, err)
		}
	}
	if s.data.Clients == nil {
		s.data.Clients = map[string]registeredClient{}
	}
	if s.data.Grants == nil {
		s.data.Grants = map[string]*grant{}
	}
	s.reindexLocked(now())
	return s, nil
}

// reindexLocked 丢掉过期的授权、令牌，重建索引。
func (s *store) reindexLocked(now time.Time) {
	s.access, s.refresh, s.used = map[string]string{}, map[string]string{}, map[string]string{}
	for id, g := range s.data.Grants {
		if !g.alive(now) {
			delete(s.data.Grants, id)
			continue
		}
		kept := g.Access[:0]
		for _, a := range g.Access {
			if now.Before(a.ExpiresAt) {
				kept = append(kept, a)
				s.access[a.Hash] = id
			}
		}
		g.Access = kept
		for _, h := range g.RefreshHashes {
			s.refresh[h] = id
		}
		for _, u := range g.UsedRefresh {
			s.used[u.Hash] = id
		}
	}
}

func (g *grant) alive(now time.Time) bool {
	return now.Before(g.RefreshExpiresAt) && now.Before(g.CreatedAt.Add(grantMaxAge))
}

func (s *store) commitLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// snapshot 拷一份 data，写盘失败时整份退回。
func (s *store) snapshotLocked() storeFile {
	out := storeFile{Clients: make(map[string]registeredClient, len(s.data.Clients)), Grants: make(map[string]*grant, len(s.data.Grants))}
	for k, v := range s.data.Clients {
		out.Clients[k] = v
	}
	for k, v := range s.data.Grants {
		cp := *v
		cp.Access = append([]accessToken(nil), v.Access...)
		cp.RefreshHashes = append([]string(nil), v.RefreshHashes...)
		cp.UsedRefresh = append([]usedRefresh(nil), v.UsedRefresh...)
		out.Grants[k] = &cp
	}
	return out
}

func (s *store) mutate(fn func(now time.Time) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	before := s.snapshotLocked()
	if err := fn(now); err != nil {
		s.data = before
		s.reindexLocked(now)
		return err
	}
	s.reindexLocked(now)
	if err := s.commitLocked(); err != nil {
		s.data = before
		s.reindexLocked(now)
		return err
	}
	return nil
}

func (s *store) create(g *grant, accessHash, refreshHash string, now time.Time) error {
	g.RefreshHashes = []string{refreshHash}
	g.RefreshExpiresAt = now.Add(refreshIdleTTL)
	g.Access = []accessToken{{Hash: accessHash, ExpiresAt: now.Add(accessTTL)}}
	return s.mutate(func(time.Time) error {
		s.data.Grants[g.ID] = g
		return nil
	})
}

// rotate 用刷新令牌换一对新令牌：用掉的那个作废、记进 UsedRefresh。用掉的在 refreshGrace 内再来（同一台电脑上
// 几个进程同时刷新），也给一个新的，各自的都认；过了再用就是被盗用，整份吊销。
func (s *store) rotate(refreshHash, clientID, accessHash, newRefreshHash string, now time.Time) (grant, error) {
	var out grant
	reused := false
	err := s.mutate(func(time.Time) error {
		id, current := s.refresh[refreshHash]
		if !current {
			usedID, ok := s.used[refreshHash]
			if !ok {
				return errors.New("mcpauth: 刷新令牌不认识")
			}
			g := s.data.Grants[usedID]
			if g == nil || !g.usedWithin(refreshHash, now, refreshGrace) {
				delete(s.data.Grants, usedID)
				reused = true
				return nil
			}
			id = usedID
		}
		g := s.data.Grants[id]
		if g == nil || !g.alive(now) || g.ClientID != clientID {
			return errors.New("mcpauth: 授权已失效")
		}
		if current {
			kept := g.RefreshHashes[:0]
			for _, h := range g.RefreshHashes {
				if h != refreshHash {
					kept = append(kept, h)
				}
			}
			g.RefreshHashes = kept
			g.UsedRefresh = append(g.UsedRefresh, usedRefresh{Hash: refreshHash, At: now})
			if len(g.UsedRefresh) > maxUsedRefresh {
				g.UsedRefresh = g.UsedRefresh[len(g.UsedRefresh)-maxUsedRefresh:]
			}
		}
		g.RefreshHashes = append(g.RefreshHashes, newRefreshHash)
		if len(g.RefreshHashes) > maxRefreshTokens {
			g.RefreshHashes = g.RefreshHashes[len(g.RefreshHashes)-maxRefreshTokens:]
		}
		g.RefreshExpiresAt = now.Add(refreshIdleTTL)
		g.LastUsedAt = now
		g.Access = append(g.Access, accessToken{Hash: accessHash, ExpiresAt: now.Add(accessTTL)})
		if len(g.Access) > maxAccessTokens {
			g.Access = g.Access[len(g.Access)-maxAccessTokens:]
		}
		out = *g
		return nil
	})
	switch {
	case err != nil:
		return grant{}, err
	case reused:
		return grant{}, errRefreshReused
	}
	return out, nil
}

func (g *grant) usedWithin(hash string, now time.Time, grace time.Duration) bool {
	for _, u := range g.UsedRefresh {
		if u.Hash == hash {
			return now.Sub(u.At) <= grace
		}
	}
	return false
}

func (s *store) revoke(id string) error {
	return s.mutate(func(time.Time) error {
		delete(s.data.Grants, id)
		return nil
	})
}

// byAccess 访问令牌对应的授权和令牌到期时间。
func (s *store) byAccess(accessHash string, now time.Time) (grant, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.access[accessHash]
	if !ok {
		return grant{}, time.Time{}, false
	}
	g := s.data.Grants[id]
	if g == nil || !g.alive(now) {
		return grant{}, time.Time{}, false
	}
	for _, a := range g.Access {
		if a.Hash == accessHash && now.Before(a.ExpiresAt) {
			return *g, a.ExpiresAt, true
		}
	}
	return grant{}, time.Time{}, false
}

func (s *store) grantByAnyToken(h string, now time.Time) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, index := range []map[string]string{s.access, s.refresh, s.used} {
		if id, ok := index[h]; ok {
			return id, true
		}
	}
	return "", false
}

// touch 记「最近用过」：内存里每次都记，盘最多隔 every 写一次。
func (s *store) touch(id string, now time.Time, every time.Duration) {
	s.mu.Lock()
	g := s.data.Grants[id]
	if g == nil {
		s.mu.Unlock()
		return
	}
	g.LastUsedAt = now
	due := now.Sub(s.saved[id]) >= every
	if due {
		s.saved[id] = now
		_ = s.commitLocked()
	}
	s.mu.Unlock()
}

func (s *store) register(c Client, now time.Time) error {
	return s.mutate(func(time.Time) error {
		if len(s.data.Clients) >= maxRegisteredClients {
			// 满了丢最早注册、现在没有授权在用的那些。
			inUse := map[string]bool{}
			for _, g := range s.data.Grants {
				inUse[g.ClientID] = true
			}
			var idle []registeredClient
			for _, rc := range s.data.Clients {
				if !inUse[rc.ID] {
					idle = append(idle, rc)
				}
			}
			sort.Slice(idle, func(i, j int) bool { return idle[i].CreatedAt.Before(idle[j].CreatedAt) })
			for i := 0; i < len(idle) && len(s.data.Clients) >= maxRegisteredClients; i++ {
				delete(s.data.Clients, idle[i].ID)
			}
			if len(s.data.Clients) >= maxRegisteredClients {
				return errors.New("mcpauth: 注册的客户端太多")
			}
		}
		s.data.Clients[c.ID] = registeredClient{ID: c.ID, Name: c.Name, RedirectURIs: c.RedirectURIs, CreatedAt: now}
		return nil
	})
}

func (s *store) client(id string) (Client, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rc, ok := s.data.Clients[id]
	if !ok {
		return Client{}, false
	}
	return Client{ID: rc.ID, Name: rc.Name, RedirectURIs: append([]string(nil), rc.RedirectURIs...)}, true
}
