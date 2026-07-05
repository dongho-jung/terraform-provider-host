package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func readProtectedFile(path string) ([]byte, bool, error) {
	content, err := os.ReadFile(path)
	if err == nil {
		return content, true, nil
	}
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func writeProtectedFile(ctx context.Context, sudoPath string, path string, content []byte, mode os.FileMode) error {
	if os.Geteuid() == 0 {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, mode); err != nil {
			return err
		}
		return os.Chmod(path, mode)
	}
	if sudoPath == "" {
		return fmt.Errorf("writing %s requires root privileges, but sudo was not found in PATH", path)
	}
	if err := authenticateHostSystemSudo(ctx, sudoPath, "write", path); err != nil {
		return err
	}
	if err := runProtectedFileCommand(ctx, sudoPath, nil, "mkdir", "-p", filepath.Dir(path)); err != nil {
		return err
	}
	if err := runProtectedFileCommand(ctx, sudoPath, bytes.NewReader(content), "tee", path); err != nil {
		return err
	}
	return runProtectedFileCommand(ctx, sudoPath, nil, "chmod", fmt.Sprintf("%04o", mode.Perm()), path)
}

func removeProtectedFile(ctx context.Context, sudoPath string, path string) error {
	if os.Geteuid() == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if sudoPath == "" {
		return fmt.Errorf("removing %s requires root privileges, but sudo was not found in PATH", path)
	}
	if err := authenticateHostSystemSudo(ctx, sudoPath, "remove", path); err != nil {
		return err
	}
	return runProtectedFileCommand(ctx, sudoPath, nil, "rm", "-f", path)
}

func runProtectedFileCommand(ctx context.Context, sudoPath string, stdin io.Reader, name string, args ...string) error {
	commandArgs := append([]string{name}, args...)
	cmd := exec.CommandContext(ctx, sudoPath, commandArgs...)
	cmd.Stdin = stdin

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s failed: %w\n%s", sudoPath, strings.Join(commandArgs, " "), err, strings.TrimSpace(stderr.String()))
	}

	return nil
}
