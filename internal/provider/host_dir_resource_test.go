package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestParseHostDirMode(t *testing.T) {
	t.Parallel()

	mode, err := parseHostDirMode("0750")
	if err != nil {
		t.Fatalf("parseHostDirMode: %s", err)
	}
	if mode != 0o750 {
		t.Fatalf("got %04o, want 0750", mode)
	}

	for _, value := range []string{"755", "0999", "1000", "abcd"} {
		if _, err := parseHostDirMode(value); err == nil {
			t.Fatalf("expected invalid mode %q", value)
		}
	}
}

func TestSyncHostDirCreatesDirectory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "dir")
	state, err := syncHostDir(HostDirResourceModel{
		Path:            types.StringValue(path),
		Mode:            types.StringValue("0750"),
		RecursiveDelete: types.BoolValue(false),
	})
	if err != nil {
		t.Fatalf("syncHostDir: %s", err)
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat: %s", err)
	}
	if !info.IsDir() {
		t.Fatal("expected directory")
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Fatalf("mode got %04o, want 0750", got)
	}
	if state.PathResolved.ValueString() != path {
		t.Fatalf("path_resolved got %q, want %q", state.PathResolved.ValueString(), path)
	}
}

func TestDeleteHostDirRefusesNonEmptyDirectoryByDefault(t *testing.T) {
	t.Parallel()

	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, "file"), []byte("content"), 0o600); err != nil {
		t.Fatalf("write file: %s", err)
	}

	err := deleteHostDir(HostDirResourceModel{
		Path:            types.StringValue(path),
		RecursiveDelete: types.BoolValue(false),
	})
	if err == nil {
		t.Fatal("expected non-empty directory error")
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Fatalf("directory should remain: %s", statErr)
	}
}
