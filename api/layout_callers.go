package main

// layout_callers.go — 排版接口的钥匙：谁能调、停没停用。
//
// 调用方名单在 FBIF 中台「接口调用方」表，进排版工具的配置包（GET /api/config/bundle/layout，
// hub_bundle.go），条目 api_callers，一行一家：code、name、owner、status、key_sha256。表里只存钥匙的
// sha256 指纹（32 字节随机钥匙算出来的，倒推不回钥匙），钥匙本身只在发钥匙那一刻打印一次
// （cd api && go run . -new-layout-key），交给调用方后就不再出现。
//
// 内存里按指纹查，常量时间比较；status「在用」才放行，其余一律 key_disabled。配置包取不到时留最后一份，
// 但离上次和中台核对成功超过 10 分钟就全部 503 auth_unavailable：宁可拒、不放（停用要及时生效）。
// 开机还没核对成功过、或配置包里还没有这个条目（中台还没加表），同样 auth_unavailable。
// 旧的 RENDER_TOKEN 只开旧路由（/api/render、/api/html-to-blocks），这里的钥匙只开 /api/layout/v1/*。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	layoutKeyPrefix          = "flk_"
	layoutKeyRandomBytes     = 32
	layoutCallerActive       = "在用"
	layoutCallersMaxStale    = 10 * time.Minute
	layoutCallersCheckEvery  = 20 * time.Second
	layoutCallersRefreshTime = 15 * time.Second
)

// layoutCallerRow 配置包 items.api_callers 的一行（中台「接口调用方」表）。
type layoutCallerRow struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	Status    string `json:"status"`
	KeySHA256 string `json:"key_sha256"`
}

// layoutCaller 处理链看到的调用方：表里那一行的编号和名字，不带钥匙。
type layoutCaller struct {
	Code  string
	Name  string
	Owner string
}

type layoutCallerEntry struct {
	caller layoutCaller
	status string
	digest [sha256.Size]byte
}

// layoutCallerSource 取调用方名单。maxAge 内和中台核对过就直接给那一份，不再问；present=false 表示配置包里没有这个条目。
type layoutCallerSource interface {
	LayoutCallers(ctx context.Context, maxAge time.Duration) (rows []layoutCallerRow, present bool, checkedAt time.Time, err error)
}

// layoutCallerDirectory 内存里的调用方名单。
type layoutCallerDirectory struct {
	source layoutCallerSource
	now    func() time.Time

	mu        sync.RWMutex
	entries   []layoutCallerEntry
	have      bool      // 至少拿到过一份
	checkedAt time.Time // 上次和中台核对成功
	note      string    // 最近一次没核对成的原因，只进日志
}

func newLayoutCallerDirectory(source layoutCallerSource, now func() time.Time) *layoutCallerDirectory {
	if now == nil {
		now = time.Now
	}
	return &layoutCallerDirectory{source: source, now: now}
}

// layoutKeyFingerprint 钥匙的指纹：整串钥匙（含 flk_ 前缀）的 sha256，小写十六进制。表里填的就是它。
func layoutKeyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// newLayoutKey 发一把新钥匙：flk_ 加 32 字节随机数的 base64url，同时给出指纹。
func newLayoutKey() (key, fingerprint string, err error) {
	raw := make([]byte, layoutKeyRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	key = layoutKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return key, layoutKeyFingerprint(key), nil
}

// layoutKeyShapeOK 只看长相：前缀对、长度对、字符都是 base64url。长相不对的直接 unauthorized，不去比表。
func layoutKeyShapeOK(key string) bool {
	body, ok := strings.CutPrefix(key, layoutKeyPrefix)
	if !ok || len(body) != base64.RawURLEncoding.EncodedLen(layoutKeyRandomBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(body)
	return err == nil
}

func parseLayoutCallerRows(rows []layoutCallerRow) ([]layoutCallerEntry, []string) {
	var entries []layoutCallerEntry
	var problems []string
	seenCode := map[string]bool{}
	seenDigest := map[[sha256.Size]byte]int{}
	for i, row := range rows {
		code := strings.TrimSpace(row.Code)
		raw, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(row.KeySHA256)))
		if code == "" || err != nil || len(raw) != sha256.Size {
			problems = append(problems, fmt.Sprintf("第 %d 行缺编号或钥匙指纹不是 64 位十六进制，跳过", i+1))
			continue
		}
		if seenCode[code] {
			problems = append(problems, fmt.Sprintf("编号 %s 重复，后一行跳过", code))
			continue
		}
		var digest [sha256.Size]byte
		copy(digest[:], raw)
		if first, dup := seenDigest[digest]; dup {
			// 两行同一把钥匙分不清是谁，两行都不认。
			problems = append(problems, fmt.Sprintf("编号 %s 和 %s 填了同一个钥匙指纹，两行都不认", entries[first].caller.Code, code))
			entries[first].status = "指纹重复"
			continue
		}
		seenCode[code] = true
		seenDigest[digest] = len(entries)
		entries = append(entries, layoutCallerEntry{
			caller: layoutCaller{Code: code, Name: strings.TrimSpace(row.Name), Owner: strings.TrimSpace(row.Owner)},
			status: strings.TrimSpace(row.Status),
			digest: digest,
		})
	}
	return entries, problems
}

// refresh 和中台核对一次（maxAge 内核对过就用那一份）。拿不到就留着上一份，由 authenticate 按核对时间决定放不放。
func (d *layoutCallerDirectory) refresh(ctx context.Context, maxAge time.Duration) error {
	if d.source == nil {
		return d.recordProblem(errors.New("没有接中台配置包"))
	}
	rows, present, checkedAt, err := d.source.LayoutCallers(ctx, maxAge)
	if err != nil {
		return d.recordProblem(fmt.Errorf("读不到中台配置包：%w", err))
	}
	if !present {
		return d.recordProblem(errors.New("中台配置包里还没有「接口调用方」（api_callers）这一项"))
	}
	entries, problems := parseLayoutCallerRows(rows)
	d.mu.Lock()
	changed := !d.have || len(entries) != len(d.entries)
	d.entries, d.have, d.checkedAt = entries, true, checkedAt
	recovered := d.note != ""
	d.note = ""
	d.mu.Unlock()
	if changed || recovered {
		log.Printf("layout-api: 调用方名单 %d 家（中台核对于 %s）", len(entries), checkedAt.Format(time.RFC3339))
		for _, problem := range problems {
			log.Printf("layout-api: 调用方名单：%s", problem)
		}
	}
	return nil
}

func (d *layoutCallerDirectory) recordProblem(err error) error {
	d.mu.Lock()
	changed := d.note != err.Error()
	d.note = err.Error()
	d.mu.Unlock()
	if changed {
		log.Printf("layout-api: 调用方名单没核对成：%v", err)
	}
	return err
}

// start 开机先核对一次，之后每 20 秒看一眼：配置包已有别处（外发账号）每分钟在取，一分钟内取过就直接用那一份。
func (d *layoutCallerDirectory) start() {
	go func() {
		check := func() {
			ctx, cancel := context.WithTimeout(context.Background(), layoutCallersRefreshTime)
			defer cancel()
			_ = d.refresh(ctx, hubRefreshEvery)
		}
		check()
		ticker := time.NewTicker(layoutCallersCheckEvery)
		defer ticker.Stop()
		for range ticker.C {
			check()
		}
	}()
}

// authenticate 认钥匙。核对过期、从没拿到过名单都按 auth_unavailable；不认识的钥匙 unauthorized；不在用 key_disabled。
func (d *layoutCallerDirectory) authenticate(key string) (layoutCaller, *layoutError) {
	key = strings.TrimSpace(key)
	d.mu.RLock()
	have, checkedAt, entries := d.have, d.checkedAt, d.entries
	d.mu.RUnlock()
	if !have {
		return layoutCaller{}, layoutErr(layoutErrAuthUnavailable)
	}
	if d.now().Sub(checkedAt) > layoutCallersMaxStale {
		return layoutCaller{}, layoutErr(layoutErrAuthUnavailable)
	}
	if !layoutKeyShapeOK(key) {
		return layoutCaller{}, layoutErr(layoutErrUnauthorized)
	}
	digest := sha256.Sum256([]byte(key))
	// 逐行常量时间比，不提前退出：比对耗时与钥匙在第几行、对上几位都无关。
	found := -1
	for i := range entries {
		if subtle.ConstantTimeCompare(digest[:], entries[i].digest[:]) == 1 {
			found = i
		}
	}
	if found < 0 {
		return layoutCaller{}, layoutErr(layoutErrUnauthorized)
	}
	if entries[found].status != layoutCallerActive {
		return layoutCaller{}, layoutErr(layoutErrKeyDisabled)
	}
	return entries[found].caller, nil
}

// hubLayoutCallerSource 用排版工具已有的中台配置包客户端取名单（和外发账号同一个客户端、同一个 ETag）。
type hubLayoutCallerSource struct{}

func (hubLayoutCallerSource) LayoutCallers(ctx context.Context, maxAge time.Duration) ([]layoutCallerRow, bool, time.Time, error) {
	client, err := layoutHubClient()
	if err != nil {
		return nil, false, time.Time{}, err
	}
	return client.LayoutCallers(ctx, maxAge)
}

// runNewLayoutKey 给 -new-layout-key 用：打印一把新钥匙和它的指纹，钥匙只出现这一次。
func runNewLayoutKey() (string, error) {
	key, fingerprint, err := newLayoutKey()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("钥匙（只显示这一次，复制给调用方，别存在别处）：\n")
	b.WriteString(key + "\n\n")
	b.WriteString("指纹（填进中台「接口调用方」表这一行的 key_sha256，状态填「在用」）：\n")
	b.WriteString(fingerprint + "\n")
	return b.String(), nil
}
