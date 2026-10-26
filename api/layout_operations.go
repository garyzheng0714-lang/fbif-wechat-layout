package main

// layout_operations.go — 排版接口每个请求都是内存里的一个操作。
//
// 成品只算一次、存在内存里：排好后留 15 分钟，所有结果合计不超过 256 MB，超了先挤掉最早排好的；
// GET 只读存好的那份（和 POST 直接回的逐字节一样），绝不重排、不再转存图片。
// 只在内存：进程重启后旧编号一律 404 operation_lost（编号里带着这次开机的标记，认得出是上次开机发的），
// 过期、被挤掉的回 404 operation_expired，别家钥匙的编号、编不出来的编号回 404 operation_not_found。

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	layoutStateQueued  = "queued"
	layoutStateRunning = "running"

	layoutOperationPrefix  = "op_"
	layoutBootIDHexLen     = 12
	layoutOperationRandLen = 22 // 16 字节 base64url
)

type layoutOperation struct {
	id        string
	caller    string
	requestID string
	created   time.Time
	done      chan struct{}
	cancel    func()

	mu       sync.Mutex
	state    string
	resp     layoutStoredResponse
	finished time.Time
}

func (op *layoutOperation) setState(state string) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.state = state
}

func (op *layoutOperation) currentState() string {
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.state
}

// result 只在 done 关闭之后调用。
func (op *layoutOperation) result() layoutStoredResponse {
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.resp
}

type layoutTombstone struct {
	caller string
	at     time.Time
}

type layoutOperationStore struct {
	mu         sync.Mutex
	boot       string
	now        func() time.Time
	ops        map[string]*layoutOperation
	bytes      int64
	maxBytes   int64
	ttl        time.Duration
	tombstones map[string]layoutTombstone
}

func newLayoutOperationStore(now func() time.Time) *layoutOperationStore {
	if now == nil {
		now = time.Now
	}
	raw := make([]byte, layoutBootIDHexLen/2)
	if _, err := rand.Read(raw); err != nil {
		panic("排版接口开机标记生成失败：" + err.Error())
	}
	return &layoutOperationStore{
		boot:       hex.EncodeToString(raw),
		now:        now,
		ops:        map[string]*layoutOperation{},
		maxBytes:   layoutResultMaxBytes,
		ttl:        layoutResultTTL,
		tombstones: map[string]layoutTombstone{},
	}
}

func (s *layoutOperationStore) newID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return layoutOperationPrefix + s.boot + base64.RawURLEncoding.EncodeToString(raw), nil
}

// parseLayoutOperationID 拆出开机标记；长相不对返回 false。
func parseLayoutOperationID(id string) (boot string, ok bool) {
	body, found := strings.CutPrefix(id, layoutOperationPrefix)
	if !found || len(body) != layoutBootIDHexLen+layoutOperationRandLen {
		return "", false
	}
	boot = body[:layoutBootIDHexLen]
	if _, err := hex.DecodeString(boot); err != nil {
		return "", false
	}
	if _, err := base64.RawURLEncoding.DecodeString(body[layoutBootIDHexLen:]); err != nil {
		return "", false
	}
	return boot, true
}

func (s *layoutOperationStore) create(caller, requestID string) (*layoutOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
	for attempt := 0; attempt < 4; attempt++ {
		id, err := s.newID()
		if err != nil {
			return nil, err
		}
		if s.ops[id] != nil {
			continue
		}
		op := &layoutOperation{
			id: id, caller: caller, requestID: requestID, created: s.now(),
			done: make(chan struct{}), state: layoutStateRunning,
		}
		s.ops[id] = op
		return op, nil
	}
	return nil, errors.New("排版编号撞了四次")
}

// finish 存下最终回答并唤醒等着的请求；然后按总量挤掉最早排好的。
func (s *layoutOperationStore) finish(op *layoutOperation, resp layoutStoredResponse) {
	now := s.now()
	op.mu.Lock()
	op.resp, op.finished = resp, now
	op.mu.Unlock()
	close(op.done)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ops[op.id] != op {
		return
	}
	s.bytes += int64(len(resp.Body))
	if s.bytes <= s.maxBytes {
		return
	}
	finished := make([]*layoutOperation, 0, len(s.ops))
	for _, candidate := range s.ops {
		if candidate != op && candidate.isFinished() {
			finished = append(finished, candidate)
		}
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].finishedAt().Before(finished[j].finishedAt()) })
	for _, oldest := range finished {
		if s.bytes <= s.maxBytes {
			break
		}
		s.forgetLocked(oldest, now)
	}
}

func (op *layoutOperation) isFinished() bool {
	select {
	case <-op.done:
		return true
	default:
		return false
	}
}

func (op *layoutOperation) finishedAt() time.Time {
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.finished
}

// forgetLocked 删掉一个排好的操作并留个记号，之后来取的人知道是过期了，不是没这回事。
func (s *layoutOperationStore) forgetLocked(op *layoutOperation, now time.Time) {
	delete(s.ops, op.id)
	s.bytes -= int64(len(op.result().Body))
	if s.bytes < 0 {
		s.bytes = 0
	}
	s.tombstones[op.id] = layoutTombstone{caller: op.caller, at: now}
	if len(s.tombstones) > layoutMaxTombstones {
		s.trimTombstonesLocked()
	}
}

func (s *layoutOperationStore) trimTombstonesLocked() {
	type stamped struct {
		id string
		at time.Time
	}
	all := make([]stamped, 0, len(s.tombstones))
	for id, tomb := range s.tombstones {
		all = append(all, stamped{id, tomb.at})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	for _, item := range all[:len(all)-layoutMaxTombstones] {
		delete(s.tombstones, item.id)
	}
}

func (s *layoutOperationStore) sweepLocked(now time.Time) {
	for _, op := range s.ops {
		if op.isFinished() && now.Sub(op.finishedAt()) >= s.ttl {
			s.forgetLocked(op, now)
		}
	}
	for id, tomb := range s.tombstones {
		if now.Sub(tomb.at) >= layoutTombstoneTTL {
			delete(s.tombstones, id)
		}
	}
}

func (s *layoutOperationStore) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
}

func (s *layoutOperationStore) startGC(every time.Duration) {
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for range ticker.C {
			s.sweep()
		}
	}()
}

// lookup 只认这把钥匙自己的编号；别家的一律说找不到，不暴露别家在排什么。
func (s *layoutOperationStore) lookup(caller, id string) (*layoutOperation, *layoutError) {
	boot, ok := parseLayoutOperationID(id)
	if !ok {
		return nil, layoutErr(layoutErrOperationNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
	if op := s.ops[id]; op != nil && op.caller == caller {
		return op, nil
	}
	if tomb, gone := s.tombstones[id]; gone && tomb.caller == caller {
		return nil, layoutErr(layoutErrOperationExpired)
	}
	if boot != s.boot {
		return nil, layoutErr(layoutErrOperationLost)
	}
	return nil, layoutErr(layoutErrOperationNotFound)
}

func (s *layoutOperationStore) storedBytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytes
}
