//go:build !windows

package provider

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLockHostFileContextCanBeCancelledWhileWaiting(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "managed")
	first, err := lockHostFile(path)
	if err != nil {
		t.Fatalf("acquire first lock: %s", err)
	}
	defer first.close()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = lockHostFileContext(ctx, path)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting lock error got %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancelled lock wait took %s", elapsed)
	}
}
