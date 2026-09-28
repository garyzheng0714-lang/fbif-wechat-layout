package main

import "sync"

// foodTalksAuthHealth 记录最近一次「有鉴权结论」的 FoodTalks 响应，供监控直接报凭证失效。
// 2026-09-24 事故：token 失效后监控 17 小时全绿，之后也只剩「对账积压」这种间接信号。
// 结论只看先后：401/JSON 403 记失效，任何 2xx（含业务 code!=0）记有效；网络错与 5xx
// 不是鉴权证据，不记录。状态只在进程内：重启后清零，下一次 FoodTalks 调用
// （后台对账最迟 30 分钟一轮）会重新得出结论。
type foodTalksAuthHealth struct {
	mu       sync.Mutex
	seq      uint64
	lastOK   uint64
	lastFail uint64
}

func (h *foodTalksAuthHealth) record(ok bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	if ok {
		h.lastOK = h.seq
	} else {
		h.lastFail = h.seq
	}
}

func (h *foodTalksAuthHealth) invalid() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastFail > h.lastOK
}
