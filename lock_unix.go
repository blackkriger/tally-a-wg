//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// withLock serializes ledger writers via an advisory flock on <path>.lock; best-effort — runs unlocked if the lock can't be taken.
func withLock(path string, fn func() error) error {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	lf, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fn()
	}
	defer lf.Close()
	if syscall.Flock(int(lf.Fd()), syscall.LOCK_EX) == nil {
		defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	}
	return fn()
}
