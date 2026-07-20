package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	providerRuntimeDirName       = "terraform-provider-host"
	providerLegacyRuntimeDirName = ".terraform-provider-host"
)

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

// providerDefaultRuntimeDirForHome keeps existing configurations on their
// historical working-directory runtime whenever it exists. New configurations
// use the stable target-home path immediately. A migration is therefore
// explicit: configure runtime_dir after copying the metadata, or move the
// legacy directory so it no longer takes precedence.
func providerDefaultRuntimeDirForHome(homeDir string) (string, error) {
	stableRuntimeDir, err := providerRuntimeDirForHome(homeDir)
	if err != nil {
		return "", err
	}
	legacyRuntimeDir, err := providerRuntimeDirForRuntime("")
	if err != nil {
		return "", err
	}
	legacyExists, err := providerRuntimeDirectoryExists(legacyRuntimeDir)
	if err != nil {
		return "", err
	}
	if legacyExists {
		return legacyRuntimeDir, nil
	}

	if _, err := providerRuntimeDirectoryExists(stableRuntimeDir); err != nil {
		return "", err
	}
	return stableRuntimeDir, nil
}

func providerRuntimeDirectoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect provider runtime directory %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("provider runtime path %q exists but is not a regular directory", path)
	}
	return true, nil
}

func providerRuntimeDirForRuntime(runtimeDir string) (string, error) {
	if runtimeDir != "" {
		return runtimeDir, nil
	}
	// Keep the historical fallback for internal callers without provider
	// configuration. Configure always supplies either the explicit override or
	// providerRuntimeDirForHome.
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current working directory: %w", err)
	}

	return filepath.Join(workingDir, providerLegacyRuntimeDirName), nil
}

func providerRuntimeSubdirForRuntime(runtimeDir string, name string) (string, error) {
	resolvedRuntimeDir, err := providerRuntimeDirForRuntime(runtimeDir)
	if err != nil {
		return "", err
	}

	return filepath.Join(resolvedRuntimeDir, name), nil
}
