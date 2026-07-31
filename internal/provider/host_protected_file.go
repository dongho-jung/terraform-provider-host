package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
		return writeHostFileAtomically(path, content, mode)
	}
	if sudoPath == "" {
		return fmt.Errorf("writing %s requires root privileges, but sudo was not found in PATH", path)
	}
	if err := authenticateHostSystemSudo(ctx, sudoPath, "write", path); err != nil {
		return err
	}
	if err := runProtectedFileCommand(ctx, sudoPath, "mkdir", "-p", filepath.Dir(path)); err != nil {
		return err
	}

	localTemp, err := os.CreateTemp("", ".terraform-provider-host-protected-*")
	if err != nil {
		return fmt.Errorf("create temporary protected file for %q: %w", path, err)
	}
	localTempPath := localTemp.Name()
	defer func() {
		_ = localTemp.Close()
		_ = os.Remove(localTempPath)
	}()
	if _, err := localTemp.Write(content); err != nil {
		return fmt.Errorf("write temporary protected file for %q: %w", path, err)
	}
	if err := localTemp.Sync(); err != nil {
		return fmt.Errorf("sync temporary protected file for %q: %w", path, err)
	}
	if err := localTemp.Close(); err != nil {
		return fmt.Errorf("close temporary protected file for %q: %w", path, err)
	}

	stageName, err := protectedFileStageName(path)
	if err != nil {
		return err
	}
	staged := false
	defer func() {
		if staged {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = runProtectedFileCommand(cleanupCtx, sudoPath, "rm", "-f", stageName)
		}
	}()
	if err := runProtectedFileCommand(
		ctx,
		sudoPath,
		"install",
		"-m",
		fmt.Sprintf("%04o", mode.Perm()),
		localTempPath,
		stageName,
	); err != nil {
		return err
	}
	staged = true
	if err := runProtectedFileCommand(ctx, sudoPath, "mv", "-f", stageName, path); err != nil {
		return err
	}
	staged = false
	return nil
}

func protectedFileStageName(path string) (string, error) {
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("read random bytes for protected file staging: %w", err)
	}
	return filepath.Join(
		filepath.Dir(path),
		"."+filepath.Base(path)+".terraform-provider-host-"+hex.EncodeToString(suffix[:]),
	), nil
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
	return runProtectedFileCommand(ctx, sudoPath, "rm", "-f", path)
}

func runProtectedFileCommand(ctx context.Context, sudoPath string, name string, args ...string) error {
	commandArgs := append([]string{"-n", "--", name}, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCommandWithExecutableBusyRetry(ctx, func() *exec.Cmd {
		stdout.Reset()
		stderr.Reset()
		cmd := exec.CommandContext(ctx, sudoPath, commandArgs...)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		return cmd
	})
	if err != nil {
		return fmt.Errorf("%s %s failed: %w\n%s", sudoPath, strings.Join(commandArgs, " "), err, strings.TrimSpace(stderr.String()))
	}

	return nil
}
