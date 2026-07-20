package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type HostSudoersValidator interface {
	Validate(context.Context, []byte) error
}

type CLIHostSudoersValidator struct {
	visudoPath string
}

func NewCLIHostSudoersValidator(visudoPath string) *CLIHostSudoersValidator {
	return &CLIHostSudoersValidator{visudoPath: visudoPath}
}

// Validate resolves visudo at operation time rather than provider configure
// time. This keeps executable discovery from becoming stale between plan and
// apply while still making validation mandatory before every sudoers write.
func (v *CLIHostSudoersValidator) Validate(ctx context.Context, content []byte) error {
	visudoPath := v.visudoPath
	if visudoPath == "" {
		var err error
		visudoPath, err = trustedHostSystemExecutable("visudo")
		if err != nil {
			return fmt.Errorf("validating sudoers rules: %w", err)
		}
	} else if !filepath.IsAbs(visudoPath) {
		return fmt.Errorf("configured visudo path must be absolute, got %q", visudoPath)
	}

	staging, err := os.CreateTemp("", "terraform-provider-host-sudoers-*")
	if err != nil {
		return fmt.Errorf("create sudoers validation file: %w", err)
	}
	stagingPath := staging.Name()
	defer os.Remove(stagingPath)
	if err := staging.Chmod(0o600); err != nil {
		staging.Close()
		return fmt.Errorf("chmod sudoers validation file: %w", err)
	}
	if _, err := staging.Write(content); err != nil {
		staging.Close()
		return fmt.Errorf("write sudoers validation file: %w", err)
	}
	if err := staging.Close(); err != nil {
		return fmt.Errorf("close sudoers validation file: %w", err)
	}

	cmd := exec.CommandContext(ctx, visudoPath, "-c", "-s", "-f", stagingPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			return fmt.Errorf("visudo validation failed: %w", err)
		}
		return fmt.Errorf("visudo validation failed: %w: %s", err, detail)
	}
	return nil
}
