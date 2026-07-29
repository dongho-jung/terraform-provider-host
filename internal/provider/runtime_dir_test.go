package provider

import (
	"path/filepath"
	"testing"
)

func TestProviderRuntimeDirForHomeUsesXDGStateConvention(t *testing.T) {
	t.Parallel()

	homeDir := filepath.Join(t.TempDir(), "target-home")
	got, err := providerRuntimeDirForHome(homeDir)
	if err != nil {
		t.Fatalf("provider runtime dir for home: %s", err)
	}

	want := filepath.Join(homeDir, ".local", "state", providerRuntimeDirName)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProviderRuntimeDirForHomeRejectsInvalidHome(t *testing.T) {
	t.Parallel()

	for _, homeDir := range []string{"", "relative/home", " /home/terraform", "/home/terraform\x00"} {
		if _, err := providerRuntimeDirForHome(homeDir); err == nil {
			t.Fatalf("providerRuntimeDirForHome(%q) unexpectedly succeeded", homeDir)
		}
	}
}

func TestProviderRuntimeSubdir(t *testing.T) {
	t.Parallel()

	runtimeDir := filepath.Join(t.TempDir(), "host-runtime")
	got, err := providerRuntimeSubdir(runtimeDir, "schedules")
	if err != nil {
		t.Fatalf("provider runtime subdirectory: %s", err)
	}
	want := filepath.Join(runtimeDir, "schedules")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProviderRuntimeSubdirRejectsInvalidRuntime(t *testing.T) {
	t.Parallel()

	for _, runtimeDir := range []string{"", "relative/runtime", " /tmp/runtime", "/tmp/runtime\x00"} {
		if _, err := providerRuntimeSubdir(runtimeDir, "schedules"); err == nil {
			t.Fatalf("providerRuntimeSubdir(%q) unexpectedly succeeded", runtimeDir)
		}
	}
}
