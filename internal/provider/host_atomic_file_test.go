package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteHostFileAtomicallyReplacesContentAndMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "managed.conf")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %s", err)
	}
	if err := writeHostFileAtomically(path, []byte("after\n"), 0o640); err != nil {
		t.Fatalf("atomic write: %s", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %s", err)
	}
	if string(content) != "after\n" {
		t.Fatalf("content got %q", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat result: %s", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode got %04o, want 0640", info.Mode().Perm())
	}
}
