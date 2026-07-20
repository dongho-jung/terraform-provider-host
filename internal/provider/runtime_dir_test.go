package provider

import (
	"os"
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

func TestProviderDefaultRuntimeDirUsesStablePathForNewConfiguration(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	homeDir := filepath.Join(t.TempDir(), "target-home")

	got, err := providerDefaultRuntimeDirForHome(homeDir)
	if err != nil {
		t.Fatalf("provider default runtime dir: %s", err)
	}
	want := filepath.Join(homeDir, ".local", "state", providerRuntimeDirName)
	if got != want {
		t.Fatalf("got %q, want stable path %q", got, want)
	}
}

func TestProviderDefaultRuntimeDirPreservesExistingLegacyRuntime(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	homeDir := filepath.Join(t.TempDir(), "target-home")
	legacyRuntimeDir := filepath.Join(workingDir, providerLegacyRuntimeDirName)
	if err := os.MkdirAll(legacyRuntimeDir, 0o700); err != nil {
		t.Fatalf("create legacy runtime: %s", err)
	}

	got, err := providerDefaultRuntimeDirForHome(homeDir)
	if err != nil {
		t.Fatalf("provider default runtime dir: %s", err)
	}
	if got != legacyRuntimeDir {
		t.Fatalf("got %q, want legacy path %q", got, legacyRuntimeDir)
	}
}

func TestProviderDefaultRuntimeDirPrefersLegacyWhenBothExist(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	homeDir := filepath.Join(t.TempDir(), "target-home")
	legacyRuntimeDir := filepath.Join(workingDir, providerLegacyRuntimeDirName)
	stableRuntimeDir := filepath.Join(homeDir, ".local", "state", providerRuntimeDirName)
	for _, path := range []string{legacyRuntimeDir, stableRuntimeDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("create runtime %q: %s", path, err)
		}
	}

	got, err := providerDefaultRuntimeDirForHome(homeDir)
	if err != nil {
		t.Fatalf("provider default runtime dir: %s", err)
	}
	if got != legacyRuntimeDir {
		t.Fatalf("got %q, want compatibility path %q", got, legacyRuntimeDir)
	}
}

func TestProviderLegacyRuntimeDirUsesWorkingDirectory(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %s", err)
	}

	got, err := providerRuntimeDirForRuntime("")
	if err != nil {
		t.Fatalf("provider runtime dir: %s", err)
	}

	want := filepath.Join(workingDir, providerLegacyRuntimeDirName)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProviderRuntimeDirUsesOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "host-runtime")

	got, err := providerRuntimeDirForRuntime(override)
	if err != nil {
		t.Fatalf("provider runtime dir: %s", err)
	}
	if got != override {
		t.Fatalf("got %q, want %q", got, override)
	}
}
