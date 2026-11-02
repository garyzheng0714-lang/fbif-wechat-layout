package main

import (
	"testing"
	"time"
)

// TestHubNextWait 中台照办了等待马上再问（一秒内回的也隔满一秒）；旧中台 60 秒；拒了 5 分钟；出错 1 秒起翻倍，最长 5 分钟再加抖动。
func TestHubNextWait(t *testing.T) {
	cases := []struct {
		name     string
		outcome  hubOutcome
		failures int
		elapsed  time.Duration
		spread   float64
		want     time.Duration
	}{
		{"挂了很久才回", hubWaiting, 0, 3 * time.Hour, 1, 0},
		{"马上就回", hubWaiting, 0, 200 * time.Millisecond, 1, 800 * time.Millisecond},
		{"旧中台", hubAnswered, 0, 0, 1, time.Minute},
		{"钥匙被拒", hubDenied, 0, 0, 1, 5 * time.Minute},
		{"第一次出错", hubFailed, 1, 0, 1, time.Second},
		{"第三次出错", hubFailed, 3, 0, 1, 4 * time.Second},
		{"第九次出错", hubFailed, 9, 0, 1, 256 * time.Second},
		{"第十次出错到顶", hubFailed, 10, 0, 1, 5 * time.Minute},
		{"出错很多次", hubFailed, 40, 0, 1, 5 * time.Minute},
		{"抖动往下", hubFailed, 2, 0, 0.8, 1600 * time.Millisecond},
		{"抖动往上", hubFailed, 40, 0, 1.2, 6 * time.Minute},
	}
	for _, c := range cases {
		if got := hubNextWait(c.outcome, c.failures, c.elapsed, c.spread); got != c.want {
			t.Errorf("%s：%v，要 %v", c.name, got, c.want)
		}
	}
}
