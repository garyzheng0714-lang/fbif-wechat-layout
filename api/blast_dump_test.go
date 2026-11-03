package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BLAST_IN 是一目录的 .html，BLAST_OUT 写出每篇的正文块和外发块。两边各跑一次再比。
func TestBlastDump(t *testing.T) {
	in := os.Getenv("BLAST_IN")
	out := os.Getenv("BLAST_OUT")
	if in == "" || out == "" {
		t.Skip()
	}
	entries, err := os.ReadDir(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".html") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(in, name))
		if err != nil {
			t.Fatal(err)
		}
		title, blocks, _ := extractArticleBlocks(string(raw))
		dist, _ := outgoingDistributionBlocks(blocks, map[string]string{})
		payload := map[string]any{
			"title":        title,
			"blocks":       blocks,
			"distribution": dist,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, strings.TrimSuffix(name, ".html")+".json"), encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
