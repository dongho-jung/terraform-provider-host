package provider

import (
	"os"
	"testing"
)

func TestHostScheduleCleanupAbandonedRuntimeRemovesRuntimeDir(t *testing.T) {
	t.Parallel()

	runtimeRoot := t.TempDir()
	id := "0123456789abcdef"

	user, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	spec := HostScheduleSpec{
		ID:      id,
		User:    user,
		Command: "true",
		Every:   "30m",
		Shell:   "/bin/sh",
		Enabled: true,
	}

	status, err := hostScheduleStatusForProvider(spec, "", runtimeRoot)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if err := writeHostScheduleRuntimeFilesForProvider(spec, status, "", runtimeRoot); err != nil {
		t.Fatalf("write runtime files: %s", err)
	}
	if _, err := os.Stat(status.ScriptPath); err != nil {
		t.Fatalf("expected runtime script to exist: %s", err)
	}

	resource := &HostScheduleResource{runtimeDir: runtimeRoot}
	resource.cleanupAbandonedScheduleRuntime(id)

	if _, err := os.Stat(status.RuntimeDir); !os.IsNotExist(err) {
		t.Fatalf("expected runtime directory to be removed, got stat error %v", err)
	}
}
