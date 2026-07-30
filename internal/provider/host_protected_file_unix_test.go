//go:build !windows

package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunProtectedFileCommandDisablesFurtherSudoPrompts(t *testing.T) {
	t.Parallel()

	sudoPath := filepath.Join(t.TempDir(), "sudo")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"${0}.log\"\n"
	if err := os.WriteFile(sudoPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write mock sudo: %v", err)
	}

	if err := runProtectedFileCommand(
		t.Context(),
		sudoPath,
		"install",
		"-m",
		"0644",
		"/tmp/source",
		"/tmp/target",
	); err != nil {
		t.Fatalf("run protected command: %v", err)
	}

	log, err := os.ReadFile(sudoPath + ".log")
	if err != nil {
		t.Fatalf("read mock sudo log: %v", err)
	}
	if got := strings.TrimSpace(string(log)); got != "-n -- install -m 0644 /tmp/source /tmp/target" {
		t.Fatalf("sudo arguments got %q", got)
	}
}
