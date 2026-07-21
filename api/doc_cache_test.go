package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDocImageMemoryCacheEvictsLeastRecentlyUsedWithinBounds(t *testing.T) {
	cache := newDocImageMemoryCache(5, 2, time.Minute)
	base := time.Unix(100, 0)
	if err := cache.put("a", map[string][]byte{"a.png": []byte("12")}, base); err != nil {
		t.Fatal(err)
	}
	if err := cache.put("b", map[string][]byte{"b.png": []byte("34")}, base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.get("a", "a.png", base.Add(2*time.Second)); !ok {
		t.Fatal("expected cache hit for a")
	}
	if err := cache.put("c", map[string][]byte{"c.png": []byte("56")}, base.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	if _, ok := cache.get("b", "b.png", base.Add(4*time.Second)); ok {
		t.Fatal("least recently used entry b was not evicted")
	}
	if _, ok := cache.get("a", "a.png", base.Add(4*time.Second)); !ok {
		t.Fatal("recently used entry a was evicted")
	}
	stats := cache.stats(base.Add(4 * time.Second))
	if stats.Entries != 2 || stats.Bytes != 4 || stats.Evictions != 1 {
		t.Fatalf("cache stats = %+v", stats)
	}
}

func TestDocImageMemoryCacheRejectsTooManyImages(t *testing.T) {
	cache := newDocImageMemoryCache(maxDocCacheBytes, maxDocCacheEntries, time.Minute)
	files := make(map[string][]byte, maxDocMediaFiles+1)
	for i := 0; i <= maxDocMediaFiles; i++ {
		files[fmt.Sprintf("%03d.png", i)] = []byte{1}
	}
	if err := cache.put("too-many", files, time.Now()); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("put error = %v, want image-count rejection", err)
	}
}

func TestStripImagesFromDocxRejectsUnsafeMediaPath(t *testing.T) {
	docx := zipFixture(t, map[string][]byte{
		"word/document.xml":            []byte("<document/>"),
		"word/media/nested/attack.svg": []byte("<svg/>"),
	})
	if _, err := stripImagesFromDocx(docx); err == nil || !strings.Contains(err.Error(), "unsafe media path") {
		t.Fatalf("strip error = %v, want unsafe path rejection", err)
	}
}

func TestStripImagesFromDocxRejectsImageCountBomb(t *testing.T) {
	entries := map[string][]byte{"word/document.xml": []byte("<document/>")}
	for i := 0; i <= maxDocMediaFiles; i++ {
		entries[fmt.Sprintf("word/media/%03d.png", i)] = []byte{1}
	}
	docx := zipFixture(t, entries)
	if _, err := stripImagesFromDocx(docx); err == nil || !strings.Contains(err.Error(), "too many media files") {
		t.Fatalf("strip error = %v, want media-count rejection", err)
	}
}

func zipFixture(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
