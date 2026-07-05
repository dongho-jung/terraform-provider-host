package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"runtime"
	"strings"
)

func resolveTargetUserHomeDir(ctx context.Context, username string) (string, error) {
	if err := validateHostUserName(username); err != nil {
		return "", err
	}

	if runtime.GOOS == "darwin" {
		if home, err := darwinUserHomeDir(ctx, username); err == nil {
			return home, nil
		}
	}

	if home, err := userLookupHomeDir(username); err == nil {
		return home, nil
	}

	if home, err := getentUserHomeDir(ctx, username); err == nil {
		return home, nil
	}

	return "", fmt.Errorf("resolve home directory for user %q", username)
}

func userLookupHomeDir(username string) (string, error) {
	user, err := osuser.Lookup(username)
	if err != nil {
		return "", err
	}
	return cleanResolvedHomeDir(username, user.HomeDir)
}

func darwinUserHomeDir(ctx context.Context, username string) (string, error) {
	dsclPath := "/usr/bin/dscl"
	if _, err := os.Stat(dsclPath); err != nil {
		dsclPath = executablePath("dscl")
	}
	if dsclPath == "" {
		return "", fmt.Errorf("dscl command not found")
	}

	out, err := exec.CommandContext(ctx, dsclPath, ".", "-read", "/Users/"+username, "NFSHomeDirectory").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read macOS user home: %w%s", err, commandOutputSuffix(out))
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		value, ok := strings.CutPrefix(line, "NFSHomeDirectory:")
		if ok {
			return cleanResolvedHomeDir(username, strings.TrimSpace(value))
		}
	}
	return "", fmt.Errorf("NFSHomeDirectory not found for user %q", username)
}

func getentUserHomeDir(ctx context.Context, username string) (string, error) {
	getentPath := executablePath("getent")
	if getentPath == "" {
		return "", fmt.Errorf("getent command not found")
	}
	out, err := exec.CommandContext(ctx, getentPath, "passwd", username).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read passwd entry: %w%s", err, commandOutputSuffix(out))
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(fields) < 6 {
		return "", fmt.Errorf("passwd entry for user %q has %d fields, want at least 6", username, len(fields))
	}
	return cleanResolvedHomeDir(username, fields[5])
}

func cleanResolvedHomeDir(username string, home string) (string, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return "", fmt.Errorf("home directory for user %q is empty", username)
	}
	if strings.Contains(home, "\x00") {
		return "", fmt.Errorf("home directory for user %q must not contain NUL bytes", username)
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("home directory for user %q must be absolute, got %q", username, home)
	}
	return filepath.Clean(home), nil
}

func validateHostUserName(username string) error {
	if strings.TrimSpace(username) != username || username == "" {
		return fmt.Errorf("username must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(username, " \t\r\n:/") || strings.HasPrefix(username, "-") {
		return fmt.Errorf("username %q is invalid; it must not contain whitespace, ':' or '/' and must not start with '-'", username)
	}
	return nil
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
