package provider

import (
	"fmt"
	"path/filepath"
	"strings"
)

const providerRuntimeDirName = "terraform-provider-host"

// providerRuntimeDirForHome returns the default provider runtime directory for
// a target home. Runtime data must outlive a particular Terraform checkout, so
// the default follows the XDG state directory convention instead of living in
// the current working directory.
func providerRuntimeDirForHome(homeDir string) (string, error) {
	if strings.TrimSpace(homeDir) != homeDir || homeDir == "" {
		return "", fmt.Errorf("home directory must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.Contains(homeDir, "\x00") {
		return "", fmt.Errorf("home directory must not contain NUL bytes")
	}
	if !filepath.IsAbs(homeDir) {
		return "", fmt.Errorf("home directory must be absolute, got %q", homeDir)
	}

	return filepath.Join(filepath.Clean(homeDir), ".local", "state", providerRuntimeDirName), nil
}

func providerRuntimeSubdir(runtimeDir string, name string) (string, error) {
	if strings.TrimSpace(runtimeDir) != runtimeDir || runtimeDir == "" {
		return "", fmt.Errorf("runtime directory must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.Contains(runtimeDir, "\x00") {
		return "", fmt.Errorf("runtime directory must not contain NUL bytes")
	}
	if !filepath.IsAbs(runtimeDir) {
		return "", fmt.Errorf("runtime directory must be absolute, got %q", runtimeDir)
	}

	return filepath.Join(filepath.Clean(runtimeDir), name), nil
}
