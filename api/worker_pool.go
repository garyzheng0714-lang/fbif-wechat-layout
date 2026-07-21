package main

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

type boundedWorkerPool struct {
	queue    chan func()
	stop     chan struct{}
	stopped  atomic.Bool
	stopOnce sync.Once
	workers  sync.WaitGroup
}

func newBoundedWorkerPool(workerCount, queueCapacity int) *boundedWorkerPool {
	if workerCount < 1 {
		workerCount = 1
	}
	if queueCapacity < workerCount {
		queueCapacity = workerCount
	}
	pool := &boundedWorkerPool{
		queue: make(chan func(), queueCapacity),
		stop:  make(chan struct{}),
	}
	pool.workers.Add(workerCount)
	for index := 0; index < workerCount; index++ {
		go pool.run(index + 1)
	}
	return pool
}

func (p *boundedWorkerPool) submit(task func()) bool {
	if task == nil || p.stopped.Load() {
		return false
	}
	select {
	case p.queue <- task:
		return true
	case <-p.stop:
		return false
	}
}

func (p *boundedWorkerPool) run(workerID int) {
	defer p.workers.Done()
	for {
		select {
		case <-p.stop:
			return
		default:
		}
		select {
		case <-p.stop:
			return
		case task := <-p.queue:
			select {
			case <-p.stop:
				return
			default:
				p.runTask(workerID, task)
			}
		}
	}
}

func (p *boundedWorkerPool) runTask(workerID int, task func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("publish: worker %d recovered panic: %v\n%s", workerID, recovered, debug.Stack())
		}
	}()
	task()
}

func (p *boundedWorkerPool) shutdown(ctx context.Context) error {
	p.stopOnce.Do(func() {
		p.stopped.Store(true)
		close(p.stop)
	})
	done := make(chan struct{})
	go func() {
		p.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("worker pool shutdown: %w", ctx.Err())
	}
}
