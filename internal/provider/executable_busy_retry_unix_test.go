//go:build !windows

package provider

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRunCommandWithExecutableBusyRetry(t *testing.T) {
	t.Parallel()

	executablePath := filepath.Join(t.TempDir(), "command")
	if err := os.WriteFile(executablePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write executable: %s", err)
	}

	writer, err := os.OpenFile(executablePath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("hold executable open for writing: %s", err)
	}
	writerClosed := make(chan error, 1)
	time.AfterFunc(40*time.Millisecond, func() {
		writerClosed <- writer.Close()
	})

	attempts := 0
	ctx := t.Context()
	err = runCommandWithExecutableBusyRetry(ctx, func() *exec.Cmd {
		attempts++
		return exec.CommandContext(ctx, executablePath)
	})
	if err != nil {
		t.Fatalf("run executable after busy retry: %s", err)
	}
	if err := <-writerClosed; err != nil {
		t.Fatalf("close executable writer: %s", err)
	}
	if attempts < 2 {
		t.Fatalf("attempts got %d, want at least 2", attempts)
	}
}
