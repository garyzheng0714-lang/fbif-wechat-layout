package main

import (
	"strings"
	"testing"
	"time"
)

func envOf(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// TestJobIntervalsDefaultsWhenHubSendsNothing 中台还没下发：一律按默认跑，和总账本现在写的一样，启动日志逐个标「默认」。
func TestJobIntervalsDefaultsWhenHubSendsNothing(t *testing.T) {
	got, line := loadJobIntervals(envOf(nil))
	if got != defaultJobIntervals {
		t.Fatalf("没下发要按默认：%+v", got)
	}
	want := "间隔（总账本「定时任务和回调」）：CMS_RECONCILE_PEAK_WINDOW=08:30-18:00（默认） CMS_RECONCILE_PEAK_SECONDS=120（默认） " +
		"CMS_RECONCILE_QUIET_SECONDS=1800（默认） AUTO_DISTRIBUTE_SECONDS=600（默认） DISTRIBUTION_FOLLOW_SECONDS=60（默认） " +
		"EDIT_REPORT_CHECK_SECONDS=1800（默认） PLATFORMS_FALLBACK_SECONDS=21600（默认） " +
		"MANUAL_LOOKUP_WINDOW=09:00-21:00（默认） MANUAL_LOOKUP_SECONDS=1800（默认）"
	if line != want {
		t.Fatalf("启动日志：\n%s\n要\n%s", line, want)
	}
}

// TestJobIntervalsReadHubValues 中台下发的值换上，日志写现在按多少跑。
func TestJobIntervalsReadHubValues(t *testing.T) {
	got, line := loadJobIntervals(envOf(map[string]string{
		"CMS_RECONCILE_PEAK_WINDOW":   "07:00-23:00",
		"CMS_RECONCILE_PEAK_SECONDS":  "300",
		"CMS_RECONCILE_QUIET_SECONDS": " 3600 ",
		"AUTO_DISTRIBUTE_SECONDS":     "900",
		"DISTRIBUTION_FOLLOW_SECONDS": "120",
		"EDIT_REPORT_CHECK_SECONDS":   "3600",
		"PLATFORMS_FALLBACK_SECONDS":  "43200",
		"MANUAL_LOOKUP_WINDOW":        "08:00-22:00",
		"MANUAL_LOOKUP_SECONDS":       "900",
	}))
	want := jobIntervals{
		cmsPeak: 5 * time.Minute, cmsPeakWindow: dayWindow{7 * 60, 23 * 60}, cmsQuiet: time.Hour,
		autoDistribute: 15 * time.Minute, distributionFollow: 2 * time.Minute, editReportCheck: time.Hour,
		platformsFallback: 12 * time.Hour, manualLookup: 15 * time.Minute, manualLookupWindow: dayWindow{8 * 60, 22 * 60},
	}
	if got != want {
		t.Fatalf("换上中台的值：%+v", got)
	}
	if strings.Contains(line, "默认") || !strings.Contains(line, "CMS_RECONCILE_PEAK_WINDOW=07:00-23:00 CMS_RECONCILE_PEAK_SECONDS=300 ") {
		t.Fatalf("日志写现在按的值：%s", line)
	}
}

// TestJobIntervalsIgnoreWhatTheyCannotRead 看不懂的用默认、日志只写变量名不写原值，不退出。
func TestJobIntervalsIgnoreWhatTheyCannotRead(t *testing.T) {
	for _, raw := range []string{"abc", "59", "2592001", "1.5", "-60", "0"} {
		got, line := loadJobIntervals(envOf(map[string]string{"AUTO_DISTRIBUTE_SECONDS": raw}))
		if got != defaultJobIntervals || !strings.Contains(line, "AUTO_DISTRIBUTE_SECONDS=600（看不懂，用默认）") {
			t.Fatalf("%q 看不懂要用默认：%+v %s", raw, got, line)
		}
	}
	for _, raw := range []string{"8:30-18:00", "08:30~18:00", "08:30-08:30", "24:00-08:00", "08:60-18:00", "08:30-24:30", "08:30-25:00", "全天"} {
		got, line := loadJobIntervals(envOf(map[string]string{"CMS_RECONCILE_PEAK_WINDOW": raw}))
		if got != defaultJobIntervals || !strings.Contains(line, "CMS_RECONCILE_PEAK_WINDOW=08:30-18:00（看不懂，用默认）") || strings.Contains(line, "="+raw) {
			t.Fatalf("%q 看不懂要用默认、不写原值：%+v %s", raw, got, line)
		}
	}
	if _, line := loadJobIntervals(envOf(map[string]string{"AUTO_DISTRIBUTE_SECONDS": "60", "PLATFORMS_FALLBACK_SECONDS": "2592000"})); !strings.Contains(line+" ", "AUTO_DISTRIBUTE_SECONDS=60 ") ||
		!strings.Contains(line+" ", "PLATFORMS_FALLBACK_SECONDS=2592000 ") {
		t.Fatalf("1 分钟和 30 天两头都认：%s", line)
	}
}

// TestDayWindowAcrossMidnightAndAllDay 时段可以跨零点，结束可以是 24:00；结束那一刻不算在里面；按北京时间算。
func TestDayWindowAcrossMidnightAndAllDay(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 11, 3, hour, minute, 0, 0, beijing) }
	cases := []struct {
		window string
		now    time.Time
		inside bool
		edge   time.Duration
	}{
		{"23:00-07:00", at(23, 30), true, 7*time.Hour + 30*time.Minute},
		{"23:00-07:00", at(6, 59), true, time.Minute},
		{"23:00-07:00", at(7, 0), false, 16 * time.Hour},
		{"23:00-07:00", at(22, 30), false, 30 * time.Minute},
		{"20:00-24:00", at(23, 59), true, time.Minute},
		{"20:00-24:00", at(0, 0), false, 20 * time.Hour},
		{"00:00-24:00", at(12, 0), true, 12 * time.Hour},
		{"08:30-18:00", time.Date(2026, 11, 3, 2, 0, 0, 0, time.UTC), true, 8 * time.Hour},
	}
	for _, c := range cases {
		w, ok := parseDayWindow(c.window)
		if !ok || w.String() != c.window {
			t.Fatalf("%s 要认得：%v %s", c.window, ok, w)
		}
		if got := w.contains(c.now); got != c.inside {
			t.Errorf("%s %s：在不在时段里 %v，要 %v", c.window, c.now.In(beijing).Format("15:04"), got, c.inside)
		}
		if got := w.untilEdge(c.now); got != c.edge {
			t.Errorf("%s %s：到下一次进出时段 %v，要 %v", c.window, c.now.In(beijing).Format("15:04"), got, c.edge)
		}
	}
}

// TestCMSWaitFollowsHubWindow 人把白天时段改成跨零点的夜班：时段里用白天的间隔，出时段前提前醒。
func TestCMSWaitFollowsHubWindow(t *testing.T) {
	s, _ := loadJobIntervals(envOf(map[string]string{
		"CMS_RECONCILE_PEAK_WINDOW": "22:00-06:00", "CMS_RECONCILE_PEAK_SECONDS": "300", "CMS_RECONCILE_QUIET_SECONDS": "3600",
	}))
	at := func(hour, minute int) time.Time { return time.Date(2026, 11, 3, hour, minute, 0, 0, beijing) }
	for _, c := range []struct {
		now  time.Time
		want time.Duration
	}{
		{at(23, 0), 5 * time.Minute},
		{at(5, 58), 2 * time.Minute},
		{at(6, 0), time.Hour},
		{at(21, 40), 20 * time.Minute},
	} {
		if got := s.cmsWait(c.now); got != c.want {
			t.Errorf("%s：%v，要 %v", c.now.Format("15:04"), got, c.want)
		}
	}
}

// TestManualWaitSleepsOutsideWindow 人工发的号查链接：时段里按间隔，临近收工提前醒一次就停，时段外一觉睡到开工。
func TestManualWaitSleepsOutsideWindow(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 11, 21, hour, minute, 0, 0, beijing) }
	s := defaultJobIntervals // 09:00-21:00 每 30 分钟
	for _, c := range []struct {
		now  time.Time
		want time.Duration
	}{
		{at(9, 0), 30 * time.Minute},
		{at(20, 50), 10 * time.Minute},
		{at(21, 0), 12 * time.Hour},
		{at(3, 0), 6 * time.Hour},
	} {
		if got := s.manualWait(c.now); got != c.want {
			t.Errorf("%s：%v，要 %v", c.now.Format("15:04"), got, c.want)
		}
	}
}
