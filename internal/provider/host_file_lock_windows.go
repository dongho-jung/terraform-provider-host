//go:build windows

package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

func lockHostFileContext(ctx context.Context, path string) (*lockedHostFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return lockHostFile(path)
}

type lockedHostFile struct {
	lockFile *os.File
}

func lockHostFile(path string) (*lockedHostFile, error) {
	lockPath := hostFileLockPath(path)
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %q: %w", lockPath, err)
	}

	return &lockedHostFile{lockFile: lockFile}, nil
}

func (f *lockedHostFile) close() {
	_ = f.lockFile.Close()
}

func hostFileLockPath(path string) string {
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(os.TempDir(), "terraform-provider-host-"+hex.EncodeToString(sum[:])+".lock")
}
