package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderRuntimeDirUsesWorkingDirectory(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %s", err)
	}

	got, err := providerRuntimeDir()
	if err != nil {
		t.Fatalf("provider runtime dir: %s", err)
	}

	want := filepath.Join(workingDir, providerRuntimeDirName)
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
