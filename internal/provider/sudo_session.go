package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Terraform walks resources concurrently and streams its own progress lines to
// the same terminal, so a password prompt raised in the middle of a run is easy
// to miss. Everything in this file exists to make sudo authentication happen
// once, at a predictable moment, on a channel Terraform does not write to.

const (
	// hostSudoKeepaliveInterval stays well inside sudo's default five-minute
	// timestamp_timeout. A large refresh or apply outlives a single timestamp,
	// so without renewal an up-front authentication would still expire partway
	// through the run and prompt again.
	hostSudoKeepaliveInterval = 60 * time.Second

	// hostSudoKeepaliveTimeout bounds one renewal attempt so a wedged sudo
	// cannot leak goroutines for the life of the provider process.
	hostSudoKeepaliveTimeout = 30 * time.Second

	hostSudoBannerWidth = 72

	// hostSudoPrompt replaces sudo's default "[sudo] password for user:" line,
	// which is indistinguishable from Terraform's own output at a glance.
	hostSudoPrompt = "  password for %p: "
)

var (
	// hostSudoAuthMutex serializes prompts. Terraform's default parallelism is
	// ten, so without it several resource operations could each open the
	// terminal and interleave their prompts.
	hostSudoAuthMutex sync.Mutex

	hostSudoKeepaliveOnce sync.Once

	// hostSudoTerminalPath is a variable so tests can redirect the prompt
	// channel away from the developer's real terminal.
	hostSudoTerminalPath = "/dev/tty"
)

// authenticateHostSudo guarantees a usable sudo timestamp before a privileged
// command runs, prompting on the controlling terminal when one is needed.
// reason names the operation that requires root and is shown to the user.
func authenticateHostSudo(ctx context.Context, sudoPath string, reason string) error {
	if sudoPath == "" {
		return errors.New("sudo was not found in PATH")
	}
	if hostSudoTimestampValid(ctx, sudoPath) {
		return nil
	}

	hostSudoAuthMutex.Lock()
	defer hostSudoAuthMutex.Unlock()

	// Another operation may have authenticated while this one waited for the
	// lock, in which case there is nothing left to ask the user.
	if hostSudoTimestampValid(ctx, sudoPath) {
		return nil
	}

	terminal, err := openHostSudoTerminal()
	if err != nil {
		return fmt.Errorf("sudo authentication for %s needs a terminal, but %s could not be opened: %w. Run `sudo -v` before Terraform, or configure passwordless sudo", reason, hostSudoTerminalPath, err)
	}
	defer func() {
		_ = terminal.Close()
	}()

	if _, err := terminal.WriteString(hostSudoBanner(reason)); err != nil {
		return fmt.Errorf("write sudo prompt banner to %s: %w", hostSudoTerminalPath, err)
	}

	if err := runCommandWithExecutableBusyRetry(ctx, func() *exec.Cmd {
		cmd := exec.CommandContext(ctx, sudoPath, "-p", hostSudoPrompt, "-v")
		cmd.Stdin = terminal
		cmd.Stdout = terminal
		cmd.Stderr = terminal
		return cmd
	}); err != nil {
		_, _ = terminal.WriteString(hostSudoNotice("sudo authentication failed"))
		return fmt.Errorf("sudo authentication for %s failed: %w. Run `sudo -v` before Terraform, or configure passwordless sudo", reason, err)
	}

	_, _ = terminal.WriteString(hostSudoNotice("sudo authenticated, continuing"))
	startHostSudoKeepalive(sudoPath)
	return nil
}

// preauthenticateHostSudo authenticates while the provider is being configured,
// which Terraform does once before any resource is read or changed. That places
// the prompt ahead of the run's output instead of somewhere inside it.
//
// Failure is reported as a warning rather than an error: a host without a
// controlling terminal should still be able to run when sudo needs no password,
// and every privileged operation authenticates on demand as a fallback.
func preauthenticateHostSudo(ctx context.Context, sudoPath string) error {
	if os.Geteuid() == 0 {
		return nil
	}
	if sudoPath == "" {
		return errors.New("sudo was not found in PATH")
	}
	if err := authenticateHostSudo(ctx, sudoPath, "the privileged operations in this run"); err != nil {
		return err
	}
	startHostSudoKeepalive(sudoPath)
	return nil
}

// startHostSudoKeepalive renews the sudo timestamp in the background for the
// rest of the provider process, which Terraform terminates when the run ends.
// Renewal stops as soon as it fails, because a timestamp that has been revoked
// cannot be restored without another password.
func startHostSudoKeepalive(sudoPath string) {
	if sudoPath == "" {
		return
	}

	hostSudoKeepaliveOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(hostSudoKeepaliveInterval)
			defer ticker.Stop()

			for range ticker.C {
				ctx, cancel := context.WithTimeout(context.Background(), hostSudoKeepaliveTimeout)
				err := exec.CommandContext(ctx, sudoPath, "-n", "-v").Run()
				cancel()
				if err != nil {
					return
				}
			}
		}()
	})
}

func hostSudoTimestampValid(ctx context.Context, sudoPath string) bool {
	return runCommandWithExecutableBusyRetry(ctx, func() *exec.Cmd {
		return exec.CommandContext(ctx, sudoPath, "-n", "-v")
	}) == nil
}

func openHostSudoTerminal() (*os.File, error) {
	return os.OpenFile(hostSudoTerminalPath, os.O_RDWR, 0)
}

// hostSudoBanner frames the prompt so it stays recognizable inside a wall of
// Terraform progress lines. The leading bell asks for attention from a user who
// has looked away from a long run.
func hostSudoBanner(reason string) string {
	emphasis, reset := hostSudoTerminalStyle()
	rule := strings.Repeat("=", hostSudoBannerWidth)

	var banner strings.Builder
	banner.WriteString("\a\n")
	banner.WriteString(emphasis + rule + "\n")
	banner.WriteString("  SUDO PASSWORD REQUIRED  -  terraform-provider-host\n")
	banner.WriteString(rule + reset + "\n")
	banner.WriteString("  Root privileges are needed for:\n")
	banner.WriteString("      " + reason + "\n")
	banner.WriteString("\n")

	return banner.String()
}

func hostSudoNotice(message string) string {
	emphasis, reset := hostSudoTerminalStyle()
	return "\n" + emphasis + "  " + message + "." + reset + "\n\n"
}

// hostSudoTerminalStyle honors the NO_COLOR convention and terminals that
// cannot render escape sequences.
func hostSudoTerminalStyle() (emphasis string, reset string) {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" || os.Getenv("TERM") == "" {
		return "", ""
	}

	return "\033[1;33m", "\033[0m"
}

// hostSudoReason renders a command line for the banner.
func hostSudoReason(name string, args ...string) string {
	if len(args) == 0 {
		return name
	}

	return name + " " + strings.Join(args, " ")
}
