package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// AURPackageManager manages AUR packages through an AUR helper such as yay or
// paru. Candidate and upgrade lookups hit the AUR RPC over the network, so
// PackageStatus only performs them when includeRemote is true.
type AURPackageManager interface {
	PackageStatus(ctx context.Context, name string, includeRemote bool) (PackageStatus, error)
	InstallPackages(ctx context.Context, names []string) error
	UpgradePackages(ctx context.Context, names []string) error
	MarkUserPackages(ctx context.Context, names []string) error
	RemovePackages(ctx context.Context, names []string, autoremove bool) error
}

type CLIAURPackageManager struct {
	helperName string
	helperPath string
	vercmpPath string
	pacman     *CLIPacmanPackageManager
}

func NewCLIAURPackageManager(helperName string, helperPath string, vercmpPath string, pacman *CLIPacmanPackageManager) *CLIAURPackageManager {
	return &CLIAURPackageManager{
		helperName: helperName,
		helperPath: helperPath,
		vercmpPath: vercmpPath,
		pacman:     pacman,
	}
}

// PackageStatus reads install state and install reason from the local pacman
// database. Candidate and upgrade versions come from the AUR helper and are
// only looked up when includeRemote is true, keeping refreshes offline-safe
// for resources that ignore versions.
func (m *CLIAURPackageManager) PackageStatus(ctx context.Context, name string, includeRemote bool) (PackageStatus, error) {
	if err := validatePackageName(name); err != nil {
		return PackageStatus{}, err
	}

	status, err := m.pacman.localPackageStatus(ctx, name)
	if err != nil {
		return PackageStatus{}, err
	}

	if !includeRemote {
		return status, nil
	}

	candidateOut, candidateFound, err := m.runHelperQuery(ctx, "-Si", name)
	if err != nil {
		return PackageStatus{}, err
	}
	if candidateFound {
		status.CandidateVersion = parsePacmanInfoValue(string(candidateOut), "Version")
	}

	if status.Installed && status.CandidateVersion != "" && status.CandidateVersion != status.InstalledVersion {
		newer, err := m.candidateIsNewer(ctx, status.CandidateVersion, status.InstalledVersion)
		if err != nil {
			return PackageStatus{}, err
		}
		if newer {
			status.UpgradeVersion = status.CandidateVersion
		}
	}

	return status, nil
}

// InstallPackages builds and installs packages with the AUR helper. The helper
// runs as the invoking user (AUR helpers refuse to run as root) and escalates
// through its own internal sudo calls, so sudo credentials are primed first.
func (m *CLIAURPackageManager) InstallPackages(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}

	args := append([]string{"-S", "--needed", "--noconfirm"}, names...)
	_, err := m.runHelperMutate(ctx, args...)
	return err
}

func (m *CLIAURPackageManager) UpgradePackages(ctx context.Context, names []string) error {
	return m.InstallPackages(ctx, names)
}

func (m *CLIAURPackageManager) MarkUserPackages(ctx context.Context, names []string) error {
	return m.pacman.MarkUserPackages(ctx, names)
}

func (m *CLIAURPackageManager) RemovePackages(ctx context.Context, names []string, autoremove bool) error {
	return m.pacman.RemovePackages(ctx, names, autoremove)
}

func (m *CLIAURPackageManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

// candidateIsNewer reports whether candidate sorts after installed. pacman
// ships vercmp, which understands epoch/pkgrel ordering; when it is missing,
// any differing candidate is treated as newer.
func (m *CLIAURPackageManager) candidateIsNewer(ctx context.Context, candidate string, installed string) (bool, error) {
	if m.vercmpPath == "" {
		return true, nil
	}

	cmd := exec.CommandContext(ctx, m.vercmpPath, candidate, installed)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("%s %s %s failed: %w\n%s", m.vercmpPath, candidate, installed, err, strings.TrimSpace(stderr.String()))
	}

	result, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err != nil {
		return false, fmt.Errorf("parse vercmp output %q: %w", strings.TrimSpace(stdout.String()), err)
	}

	return result > 0, nil
}

func (m *CLIAURPackageManager) runHelperQuery(ctx context.Context, args ...string) ([]byte, bool, error) {
	out, err := m.runHelper(ctx, args...)
	if err == nil {
		return out, true, nil
	}
	if isAURNoResultError(err) {
		return nil, false, nil
	}
	return nil, false, err
}

// runHelperMutate serializes on the shared pacman mutex: the helper drives
// pacman internally, which takes the same exclusive db.lck as direct calls.
func (m *CLIAURPackageManager) runHelperMutate(ctx context.Context, args ...string) ([]byte, error) {
	if os.Geteuid() == 0 {
		return nil, fmt.Errorf("AUR helpers refuse to run as root; run Terraform as the unprivileged target user and let %s escalate through sudo itself", m.helperName)
	}

	pacmanMutateMutex.Lock()
	defer pacmanMutateMutex.Unlock()
	defer m.pacman.invalidateStatusCache()

	if m.pacman.sudoPath != "" {
		if err := m.pacman.authenticateSudo(ctx, m.helperPath, args...); err != nil {
			return nil, err
		}
	}

	return m.runHelper(ctx, args...)
}

func (m *CLIAURPackageManager) runHelper(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, m.helperPath, args...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s failed: %w\n%s", m.helperPath, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

// isAURNoResultError matches "package not found" style failures from yay and
// paru query commands, which exit 1 like pacman does.
func isAURNoResultError(err error) bool {
	if err == nil {
		return false
	}
	if isPacmanNoResultError(err) {
		return true
	}

	text := strings.ToLower(err.Error())
	return strings.Contains(text, "exit status 1") &&
		(strings.Contains(text, "not found") || strings.Contains(text, "no results found"))
}
