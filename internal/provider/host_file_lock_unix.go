//go:build !windows

package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type lockedHostFile struct {
	lockFile *os.File
}

func lockHostFile(path string) (*lockedHostFile, error) {
	lockPath := hostFileLockPath(path)
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %q: %w", lockPath, err)
	}

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("lock file %q: %w", lockPath, err)
	}

	return &lockedHostFile{lockFile: lockFile}, nil
}

func (f *lockedHostFile) close() {
	_ = syscall.Flock(int(f.lockFile.Fd()), syscall.LOCK_UN)
	_ = f.lockFile.Close()
}

func hostFileLockPath(path string) string {
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(os.TempDir(), "terraform-provider-host-"+hex.EncodeToString(sum[:])+".lock")
}
