package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type publishProcessLock struct {
	file *os.File
}

func acquirePublishProcessLock(storePath string) (*publishProcessLock, error) {
	if storePath == "" {
		return nil, errors.New("发布任务库路径为空")
	}
	lockPath := storePath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return nil, fmt.Errorf("创建任务库目录: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("打开进程锁 %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("已有另一个可写 API/worker 正在使用任务库 %s", storePath)
		}
		return nil, fmt.Errorf("锁定任务库 %s: %w", storePath, err)
	}
	if err := file.Truncate(0); err == nil {
		_, _ = file.Seek(0, 0)
		_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
		_ = file.Sync()
	}
	return &publishProcessLock{file: file}, nil
}

func (l *publishProcessLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(err, closeErr)
}
