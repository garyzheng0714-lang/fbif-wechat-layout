package main

import (
	"path/filepath"
	"testing"
)

func TestPublishProcessLockRejectsSecondWriterAndReleases(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "nested", "publish-jobs.json")
	first, err := acquirePublishProcessLock(storePath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := acquirePublishProcessLock(storePath)
	if err == nil {
		_ = second.Close()
		t.Fatal("second writer unexpectedly acquired the JSON store lock")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquirePublishProcessLock(storePath)
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}
