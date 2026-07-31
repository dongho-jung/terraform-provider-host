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
	state, err := syncHostDirForHome(HostDirResourceModel{
		Path:            types.StringValue(path),
		Mode:            types.StringValue("0750"),
		RecursiveDelete: types.BoolValue(false),
	}, t.TempDir())
	if err != nil {
		t.Fatalf("syncHostDirForHome: %s", err)
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

func TestSyncHostDirRefusesSymlinkBeforeChmod(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("mkdir target: %s", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %s", err)
	}

	_, err := syncHostDirForHome(HostDirResourceModel{
		Path:            types.StringValue(link),
		Mode:            types.StringValue("0755"),
		RecursiveDelete: types.BoolValue(false),
	}, root)
	if err == nil {
		t.Fatal("expected symbolic link error")
	}
	info, statErr := os.Stat(target)
	if statErr != nil {
		t.Fatalf("stat target: %s", statErr)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("target mode changed through symlink: got %04o, want 0700", got)
	}
}

func TestSyncHostDirRepairsUnreadableMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "managed")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("mkdir managed directory: %s", err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("remove managed directory permissions: %s", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o700); err != nil && !os.IsNotExist(err) {
			t.Errorf("restore managed directory permissions: %s", err)
		}
	})

	_, err := syncHostDirForHome(HostDirResourceModel{
		Path:            types.StringValue(path),
		Mode:            types.StringValue("0700"),
		RecursiveDelete: types.BoolValue(false),
	}, t.TempDir())
	if err != nil {
		t.Fatalf("syncHostDirForHome: %s", err)
	}
	info, statErr := os.Lstat(path)
	if statErr != nil {
		t.Fatalf("lstat managed directory: %s", statErr)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("mode got %04o, want 0700", got)
	}
}

func TestDeleteHostDirRefusesNonEmptyDirectoryByDefault(t *testing.T) {
	t.Parallel()

	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, "file"), []byte("content"), 0o600); err != nil {
		t.Fatalf("write file: %s", err)
	}

	err := deleteHostDirForHome(HostDirResourceModel{
		Path:            types.StringValue(path),
		RecursiveDelete: types.BoolValue(false),
	}, t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected non-empty directory error")
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Fatalf("directory should remain: %s", statErr)
	}
}

func TestDeleteHostDirRefusesRecursiveHomeDeletion(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeDir, "keep"), []byte("content"), 0o600); err != nil {
		t.Fatalf("write sentinel: %s", err)
	}

	err := deleteHostDirForHome(HostDirResourceModel{
		Path:            types.StringValue(homeDir),
		RecursiveDelete: types.BoolValue(true),
	}, homeDir, filepath.Join(homeDir, ".local", "state", providerRuntimeDirName))
	if err == nil {
		t.Fatal("expected protected home directory error")
	}
	if _, statErr := os.Lstat(filepath.Join(homeDir, "keep")); statErr != nil {
		t.Fatalf("home contents should remain: %s", statErr)
	}
}
