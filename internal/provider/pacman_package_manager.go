package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type CLIPacmanPackageManager struct {
	pacmanPath string
	sudoPath   string
}

func NewCLIPacmanPackageManager(pacmanPath string, sudoPath string) *CLIPacmanPackageManager {
	return &CLIPacmanPackageManager{
		pacmanPath: pacmanPath,
		sudoPath:   sudoPath,
	}
}

func (m *CLIPacmanPackageManager) PackageStatus(ctx context.Context, name string) (PackageStatus, error) {
	if err := validatePackageName(name); err != nil {
		return PackageStatus{}, err
	}

	status := PackageStatus{Name: name}

	installedOut, installed, err := m.runOptional(ctx, false, m.pacmanPath, "-Q", name)
	if err != nil {
		return PackageStatus{}, err
	}
	if installed {
		packageName, version, ok := parsePacmanPackageLine(strings.TrimSpace(string(installedOut)))
		if ok && packageName == name {
			status.Installed = true
			status.InstalledVersion = version
		}
	}

	explicitOut, err := m.run(ctx, false, m.pacmanPath, "-Qqe")
	if err != nil {
		return PackageStatus{}, err
	}
	for _, explicitName := range parsePackageNames(explicitOut) {
		if explicitName == name {
			status.ReasonUser = true
			break
		}
	}

	candidateOut, candidateFound, err := m.runOptional(ctx, false, m.pacmanPath, "-Si", name)
	if err != nil {
		return PackageStatus{}, err
	}
	if candidateFound {
		status.CandidateVersion = parsePacmanInfoValue(string(candidateOut), "Version")
	}

	upgradeOut, upgradeFound, err := m.runOptional(ctx, false, m.pacmanPath, "-Qu", name)
	if err != nil {
		return PackageStatus{}, err
	}
	if upgradeFound {
		packageName, version, ok := parsePacmanUpgradeLine(strings.TrimSpace(string(upgradeOut)))
		if ok && packageName == name {
			status.UpgradeVersion = version
		}
	}

	return status, nil
}

func (m *CLIPacmanPackageManager) InstallPackages(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}

	args := append([]string{"-S", "--needed", "--noconfirm"}, names...)
	_, err := m.run(ctx, true, m.pacmanPath, args...)
	return err
}

func (m *CLIPacmanPackageManager) UpgradePackages(ctx context.Context, names []string) error {
	return m.InstallPackages(ctx, names)
}

func (m *CLIPacmanPackageManager) MarkUserPackages(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}

	args := append([]string{"-D", "--asexplicit"}, names...)
	_, err := m.run(ctx, true, m.pacmanPath, args...)
	return err
}

func (m *CLIPacmanPackageManager) RemovePackages(ctx context.Context, names []string, autoremove bool) error {
	if len(names) == 0 {
		return nil
	}

	args := []string{"-R", "--noconfirm"}
	if autoremove {
		args = []string{"-Rns", "--noconfirm"}
	}
	args = append(args, names...)

	_, err := m.run(ctx, true, m.pacmanPath, args...)
	return err
}

func (m *CLIPacmanPackageManager) runOptional(ctx context.Context, mutate bool, name string, args ...string) ([]byte, bool, error) {
	out, err := m.run(ctx, mutate, name, args...)
	if err == nil {
		return out, true, nil
	}
	if isPacmanNoResultError(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func (m *CLIPacmanPackageManager) run(ctx context.Context, mutate bool, name string, args ...string) ([]byte, error) {
	commandName := name
	commandArgs := args

	if mutate && os.Geteuid() != 0 {
		if m.sudoPath == "" {
			return nil, fmt.Errorf("mutating pacman commands require root privileges, but sudo was not found in PATH")
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

func (m *CLIPacmanPackageManager) authenticateSudo(ctx context.Context, name string, args ...string) error {
	check := exec.CommandContext(ctx, m.sudoPath, "-n", "true")
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

func (m *CLIPacmanPackageManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

func parsePacmanPackageLine(line string) (string, string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", "", false
	}
	return fields[0], fields[1], true
}

func parsePacmanUpgradeLine(line string) (string, string, bool) {
	fields := strings.Fields(line)
	if len(fields) >= 4 && fields[2] == "->" {
		return fields[0], fields[3], true
	}
	return parsePacmanPackageLine(line)
}

func parsePacmanInfoValue(out string, key string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		name, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}

func isPacmanNoResultError(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(err.Error())
	return strings.Contains(text, "exit status 1") &&
		(strings.Contains(text, "was not found") ||
			strings.Contains(text, "package not found") ||
			strings.Contains(text, "target not found") ||
			strings.TrimSpace(strings.Split(text, "\n")[len(strings.Split(text, "\n"))-1]) == "")
}
