package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type PackageManager interface {
	PackageStatus(ctx context.Context, name string) (PackageStatus, error)
	InstallPackages(ctx context.Context, names []string) error
	UpgradePackages(ctx context.Context, names []string) error
	MarkUserPackages(ctx context.Context, names []string) error
	RemovePackages(ctx context.Context, names []string, autoremove bool) error
}

type PackageStatus struct {
	Name             string
	Installed        bool
	ReasonUser       bool
	InstalledVersion string
	CandidateVersion string
	UpgradeVersion   string
}

const (
	packageInstallReasonExplicit   = "explicit"
	packageInstallReasonDependency = "dependency"
)

// validatePackageName accepts the portable package-name character set shared
// by the supported command-line package managers. Keeping option prefixes,
// whitespace, controls, and path separators out prevents a configured name
// from changing command semantics even though commands do not use a shell.
func validatePackageName(name string) error {
	if name == "" {
		return fmt.Errorf("package name must be non-empty")
	}
	if name[0] == '-' || name[0] == '.' {
		return fmt.Errorf("package name must not start with %q", name[0])
	}

	for index := 0; index < len(name); index++ {
		character := name[index]
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '@' || character == '.' || character == '_' ||
			character == '+' || character == '-' {
			continue
		}

		return fmt.Errorf("package name contains unsupported character %q; use only ASCII letters, digits, @, ., _, +, and -", character)
	}

	return nil
}

type CLIPackageManager struct {
	dnfPath  string
	sudoPath string
}

func NewCLIPackageManager(dnfPath string, sudoPath string) *CLIPackageManager {
	return &CLIPackageManager{
		dnfPath:  dnfPath,
		sudoPath: sudoPath,
	}
}

func (m *CLIPackageManager) PackageStatus(ctx context.Context, name string) (PackageStatus, error) {
	status := PackageStatus{Name: name}

	installedOut, err := m.run(ctx, false, m.dnfPath, "-q", "repoquery", "--installed", "--queryformat", "%{name}\t%{evr}\t%{reason}\n", name)
	if err != nil {
		return PackageStatus{}, err
	}

	for _, line := range strings.Split(string(installedOut), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			continue
		}

		packageName := strings.TrimSpace(parts[0])
		if packageName != name {
			continue
		}

		status.Name = packageName
		status.Installed = true
		status.InstalledVersion = strings.TrimSpace(parts[1])
		status.ReasonUser = strings.TrimSpace(parts[2]) == "User"
		break
	}

	candidateOut, err := m.run(ctx, false, m.dnfPath, "-q", "repoquery", "--latest-limit=1", "--queryformat", "%{name}\t%{evr}\n", name)
	if err != nil {
		return PackageStatus{}, err
	}

	for _, line := range strings.Split(string(candidateOut), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			continue
		}

		packageName := strings.TrimSpace(parts[0])
		if packageName != name {
			continue
		}

		status.Name = packageName
		status.CandidateVersion = strings.TrimSpace(parts[1])
		break
	}

	upgradeOut, err := m.run(ctx, false, m.dnfPath, "-q", "repoquery", "--upgrades", "--latest-limit=1", "--queryformat", "%{name}\t%{evr}\n", name)
	if err != nil {
		return PackageStatus{}, err
	}

	for _, line := range strings.Split(string(upgradeOut), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			continue
		}

		packageName := strings.TrimSpace(parts[0])
		if packageName != name {
			continue
		}

		status.Name = packageName
		status.UpgradeVersion = strings.TrimSpace(parts[1])
		break
	}

	return status, nil
}

func (m *CLIPackageManager) InstallPackages(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}

	args := append([]string{"-y", "install"}, names...)
	_, err := m.run(ctx, true, m.dnfPath, args...)
	return err
}

func (m *CLIPackageManager) UpgradePackages(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}

	args := append([]string{"-y", "upgrade"}, names...)
	_, err := m.run(ctx, true, m.dnfPath, args...)
	return err
}

func (m *CLIPackageManager) MarkUserPackages(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}

	args := append([]string{"-y", "mark", "user"}, names...)
	_, err := m.run(ctx, true, m.dnfPath, args...)
	return err
}

func (m *CLIPackageManager) RemovePackages(ctx context.Context, names []string, autoremove bool) error {
	if len(names) == 0 {
		return nil
	}

	args := []string{"-y", "remove"}
	if !autoremove {
		args = append(args, "--no-autoremove")
	}
	args = append(args, names...)

	_, err := m.run(ctx, true, m.dnfPath, args...)
	return err
}

func (m *CLIPackageManager) run(ctx context.Context, mutate bool, name string, args ...string) ([]byte, error) {
	commandName := name
	commandArgs := args

	if mutate && os.Geteuid() != 0 {
		if m.sudoPath == "" {
			return nil, fmt.Errorf("mutating DNF commands require root privileges, but sudo was not found in PATH")
		}
		if err := m.authenticateSudo(ctx, name, args...); err != nil {
			return nil, err
		}

		commandName = m.sudoPath
		commandArgs = append([]string{name}, args...)
	}

	cmd := exec.CommandContext(ctx, commandName, commandArgs...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s failed: %w\n%s", commandName, strings.Join(commandArgs, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

func (m *CLIPackageManager) authenticateSudo(ctx context.Context, name string, args ...string) error {
	check := exec.CommandContext(ctx, m.sudoPath, "-n", "-v")
	if err := check.Run(); err == nil {
		return nil
	}

	fmt.Fprintf(os.Stderr, "\nTerraform provider host needs sudo privileges for: %s %s\n", name, strings.Join(args, " "))
	fmt.Fprintln(os.Stderr, "Enter your sudo password at the prompt below, or run `sudo -v` before `terraform apply`.")

	cmd := exec.CommandContext(ctx, m.sudoPath, "-v")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo authentication failed: %w. Run `sudo -v` before `terraform apply`, or configure passwordless sudo for local package management", err)
	}

	return nil
}

func (m *CLIPackageManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

func parsePackageNames(out []byte) []string {
	seen := map[string]struct{}{}

	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		seen[name] = struct{}{}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}

	sort.Strings(names)
	return names
}
