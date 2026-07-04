package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func processHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return home, nil
}

func resolveProviderHomeDir(path string) (string, error) {
	home, err := processHomeDir()
	if err != nil {
		return "", err
	}
	return expandHostPathWithHome(path, home)
}

func expandHostPathForHome(path string, home string) (string, error) {
	if home != "" {
		return expandHostPathWithHome(path, home)
	}

	defaultHome, err := processHomeDir()
	if err != nil {
		return "", err
	}
	return expandHostPathWithHome(path, defaultHome)
}

func expandHostPathWithHome(path string, home string) (string, error) {
	if strings.TrimSpace(path) != path || path == "" {
		return "", fmt.Errorf("path must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.Contains(path, "\x00") {
		return "", fmt.Errorf("path must not contain NUL bytes")
	}

	if path == "~" || strings.HasPrefix(path, "~/") {
		if home == "" {
			return "", fmt.Errorf("home directory must be non-empty")
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	} else if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("path %q uses unsupported ~user expansion", path)
	}

	if !filepath.IsAbs(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("resolve absolute path for %q: %w", path, err)
		}
		path = absolute
	}

	return filepath.Clean(path), nil
}
