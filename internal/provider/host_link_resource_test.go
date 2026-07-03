package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHostLinkSourceRelativeToWorkingDirectory(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)

	resolvedWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd after chdir: %s", err)
	}

	got, err := resolveHostLinkSource("./nvim")
	if err != nil {
		t.Fatalf("resolveHostLinkSource: %s", err)
	}
	want := filepath.Join(resolvedWorkingDir, "nvim")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWriteHostLinkCreatesSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %s", err)
	}

	link := filepath.Join(root, "config", "nvim")
	if err := writeHostLink(link, target); err != nil {
		t.Fatalf("writeHostLink: %s", err)
	}

	got, exists, err := readHostLinkSource(link)
	if err != nil {
		t.Fatalf("readHostLinkSource: %s", err)
	}
	if !exists {
		t.Fatal("expected link to exist")
	}
	if got != target {
		t.Fatalf("got %q, want %q", got, target)
	}
}

func TestWriteHostLinkRejectsExistingDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %s", err)
	}
	link := filepath.Join(root, "nvim")
	if err := os.Mkdir(link, 0o755); err != nil {
		t.Fatalf("mkdir link path: %s", err)
	}

	if err := writeHostLink(link, target); err == nil {
		t.Fatal("expected existing directory error")
	}
}

func TestEnsureHostLinkSourceExistsRejectsMissing(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing")
	if err := ensureHostLinkSourceExists(missing); err == nil {
		t.Fatal("expected missing source error")
	}
}

func TestDeleteHostLinkRefusesRegularDirectory(t *testing.T) {
	t.Parallel()

	link := filepath.Join(t.TempDir(), "nvim")
	if err := os.Mkdir(link, 0o755); err != nil {
		t.Fatalf("mkdir link path: %s", err)
	}

	if err := deleteHostLink(link); err == nil {
		t.Fatal("expected existing directory error")
	}
}
