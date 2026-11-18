package main

import (
	"encoding/json"
	"os"
	"testing"
)

// 图注里的滑动提示：外发去掉它（公众号上左右滑着看，外发平台竖着排）。网页那份认法用同一份样例考。
func TestStripSwipeHintFollowsSharedSamples(t *testing.T) {
	raw, err := os.ReadFile("testdata/swipe-hints.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples struct {
		Strip [][2]string `json:"strip"`
	}
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	if len(samples.Strip) == 0 {
		t.Fatal("样例是空的")
	}
	for _, s := range samples.Strip {
		if got := stripSwipeHint(s[0]); got != s[1] {
			t.Errorf("「%s」去掉提示后是「%s」，应是「%s」", s[0], got, s[1])
		}
	}
}
