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
