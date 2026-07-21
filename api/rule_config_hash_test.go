package main

import "testing"

func TestRuleConfigHashCanonicalContract(t *testing.T) {
	config := map[string]any{
		"footer": `<p>中文 & text</p>`,
		"nested": map[string]any{"z": float64(1), "a": true},
	}
	behavior := map[string]any{"regex": `^作者`}
	want := "sha256:3a5ec985cf4d715ae701e4a67bdbc810e6a348a47803949d95cfe70faa061078"
	if got := ruleConfigHash(config, behavior); got != want {
		t.Fatalf("ruleConfigHash=%q want=%q", got, want)
	}
}
