package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedWorkerPoolCapsConcurrency(t *testing.T) {
	pool := newBoundedWorkerPool(2, 8)
	var active atomic.Int32
	var maximum atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 6)
	var done sync.WaitGroup
	done.Add(6)

	for range 6 {
		if !pool.submit(func() {
			defer done.Done()
			current := active.Add(1)
			for {
				old := maximum.Load()
				if current <= old || maximum.CompareAndSwap(old, current) {
					break
				}
			}
			started <- struct{}{}
			<-release
			active.Add(-1)
		}) {
			t.Fatal("task submission unexpectedly rejected")
		}
	}

	<-started
	<-started
	if got := maximum.Load(); got != 2 {
		t.Fatalf("maximum concurrency = %d, want 2", got)
	}
	close(release)
	done.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestBoundedWorkerPoolSurvivesTaskPanic(t *testing.T) {
	pool := newBoundedWorkerPool(1, 2)
	done := make(chan struct{})
	if !pool.submit(func() { panic("injected worker panic") }) {
		t.Fatal("panic task submission failed")
	}
	if !pool.submit(func() { close(done) }) {
		t.Fatal("follow-up task submission failed")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not continue after task panic")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestBoundedWorkerPoolRejectsAfterShutdown(t *testing.T) {
	pool := newBoundedWorkerPool(1, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if pool.submit(func() {}) {
		t.Fatal("task accepted after shutdown")
	}
}
