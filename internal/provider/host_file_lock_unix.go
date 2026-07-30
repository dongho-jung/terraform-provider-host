//go:build !windows

package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type lockedHostFile struct {
	lockFile *os.File
}

func lockHostFile(path string) (*lockedHostFile, error) {
	return lockHostFileContext(context.Background(), path)
}

func lockHostFileContext(ctx context.Context, path string) (*lockedHostFile, error) {
	lockPath := hostFileLockPath(path)
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %q: %w", lockPath, err)
	}

	err = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return &lockedHostFile{lockFile: lockFile}, nil
	}
	if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
		_ = lockFile.Close()
		return nil, fmt.Errorf("lock file %q: %w", lockPath, err)
	}

	retry := time.NewTicker(25 * time.Millisecond)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = lockFile.Close()
			return nil, fmt.Errorf("lock file %q: %w", lockPath, ctx.Err())
		case <-retry.C:
		}

		err = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &lockedHostFile{lockFile: lockFile}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = lockFile.Close()
			return nil, fmt.Errorf("lock file %q: %w", lockPath, err)
		}
	}
}

func (f *lockedHostFile) close() {
	_ = syscall.Flock(int(f.lockFile.Fd()), syscall.LOCK_UN)
	_ = f.lockFile.Close()
}

func hostFileLockPath(path string) string {
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(os.TempDir(), "terraform-provider-host-"+hex.EncodeToString(sum[:])+".lock")
}
