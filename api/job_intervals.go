package main

// job_intervals.go — 后台循环多久跑一次。间隔只在 FBIF 中台总账本「定时任务和回调」改（2026-11-03 用户定）：
// 中台经变量清单下发进 /etc/fbif-hub/env/layout.env，本机代理在重启时段重启本服务，启动时读一次。
// defaultJobIntervals 和表里现在写的一样，只在变量还没下发时用；看不懂的也用它，启动日志里写明，不退出。
// 时段按北京时间算，不看服务器时区。

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var beijing = time.FixedZone("CST", 8*3600)

// dayWindow 北京时间每天的一个时段 [from, to)，记当天第几分钟：to 可以是 1440（24:00），from > to 是跨零点。
type dayWindow struct{ from, to int }

var dayWindowPattern = regexp.MustCompile(`^(\d\d):(\d\d)-(\d\d):(\d\d)$`)

// parseDayWindow 读中台写的 HH:MM-HH:MM：结束可以是 24:00，起止不能一样。
func parseDayWindow(raw string) (dayWindow, bool) {
	m := dayWindowPattern.FindStringSubmatch(raw)
	if m == nil {
		return dayWindow{}, false
	}
	n := make([]int, 4)
	for i := range n {
		n[i], _ = strconv.Atoi(m[i+1])
	}
	w := dayWindow{n[0]*60 + n[1], n[2]*60 + n[3]}
	ok := n[0] < 24 && n[1] < 60 && n[3] < 60 && (n[2] < 24 || w.to == 1440) && w.from != w.to
	return w, ok
}

func (w dayWindow) String() string {
	return fmt.Sprintf("%02d:%02d-%02d:%02d", w.from/60, w.from%60, w.to/60, w.to%60)
}

func (w dayWindow) contains(t time.Time) bool {
	t = t.In(beijing)
	m := t.Hour()*60 + t.Minute()
	if w.from < w.to {
		return m >= w.from && m < w.to
	}
	return m >= w.from || m < w.to
}

// untilEdge 到下一次进出这个时段还有多久。
func (w dayWindow) untilEdge(t time.Time) time.Duration {
	t = t.In(beijing)
	y, mo, d := t.Date()
	midnight := time.Date(y, mo, d, 0, 0, 0, 0, beijing)
	var next time.Duration
	for _, minute := range []int{w.from, w.to, w.from + 1440, w.to + 1440} {
		if until := midnight.Add(time.Duration(minute) * time.Minute).Sub(t); until > 0 && (next == 0 || until < next) {
			next = until
		}
	}
	return next
}

// jobIntervals 一个字段对应总账本「定时任务和回调」的一行。
type jobIntervals struct {
	cmsPeak            time.Duration // 后台发布对账（白天）
	cmsPeakWindow      dayWindow     // 后台发布对账（白天）的时段
	cmsQuiet           time.Duration // 后台发布对账（其余时间）
	autoDistribute     time.Duration // 自动分发
	distributionFollow time.Duration // 分发进度跟进
	editReportCheck    time.Duration // 编辑周报检查
	platformsFallback  time.Duration // 平台限制兜底
}

var defaultJobIntervals = jobIntervals{
	cmsPeak:            2 * time.Minute,
	cmsPeakWindow:      dayWindow{8*60 + 30, 18 * 60},
	cmsQuiet:           30 * time.Minute,
	autoDistribute:     10 * time.Minute,
	distributionFollow: time.Minute,
	editReportCheck:    30 * time.Minute,
	platformsFallback:  6 * time.Hour,
}

// intervals 进程里实际用的间隔：main 在起任何循环之前用 loadJobIntervals 换上一次，之后只读。
var intervals = defaultJobIntervals

// intervalVar 中台下发的一个变量：set 读进 jobIntervals，看不懂回 false；show 是现在的值，写法和变量一样。
type intervalVar struct {
	env  string
	set  func(raw string) bool
	show func() string
}

func secondsVar(env string, into *time.Duration) intervalVar {
	return intervalVar{env, func(raw string) bool {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 60 || n > 2592000 {
			return false
		}
		*into = time.Duration(n) * time.Second
		return true
	}, func() string { return strconv.Itoa(int(into.Seconds())) }}
}

func (s *jobIntervals) vars() []intervalVar {
	return []intervalVar{
		{"CMS_RECONCILE_PEAK_WINDOW", func(raw string) bool {
			w, ok := parseDayWindow(raw)
			if ok {
				s.cmsPeakWindow = w
			}
			return ok
		}, func() string { return s.cmsPeakWindow.String() }},
		secondsVar("CMS_RECONCILE_PEAK_SECONDS", &s.cmsPeak),
		secondsVar("CMS_RECONCILE_QUIET_SECONDS", &s.cmsQuiet),
		secondsVar("AUTO_DISTRIBUTE_SECONDS", &s.autoDistribute),
		secondsVar("DISTRIBUTION_FOLLOW_SECONDS", &s.distributionFollow),
		secondsVar("EDIT_REPORT_CHECK_SECONDS", &s.editReportCheck),
		secondsVar("PLATFORMS_FALLBACK_SECONDS", &s.platformsFallback),
	}
}

// loadJobIntervals 读中台下发的间隔，回读到的间隔和给启动日志的一行：每个变量现在按多少跑，
// 没下发的标「默认」，看不懂的标「看不懂，用默认」（不写原值）。
func loadJobIntervals(getenv func(string) string) (jobIntervals, string) {
	got := defaultJobIntervals
	parts := []string{}
	for _, v := range got.vars() {
		note := ""
		switch raw := strings.TrimSpace(getenv(v.env)); {
		case raw == "":
			note = "（默认）"
		case !v.set(raw):
			note = "（看不懂，用默认）"
		}
		parts = append(parts, v.env+"="+v.show()+note)
	}
	return got, "间隔（总账本「定时任务和回调」）：" + strings.Join(parts, " ")
}

// cmsWait 后台发布对账这一轮之后隔多久：时段里用白天的间隔，其余用另一个；临近进出时段提前醒，不睡过点。
func (s jobIntervals) cmsWait(now time.Time) time.Duration {
	interval := s.cmsQuiet
	if s.cmsPeakWindow.contains(now) {
		interval = s.cmsPeak
	}
	return min(interval, s.cmsPeakWindow.untilEdge(now))
}
