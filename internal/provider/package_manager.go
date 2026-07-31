package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"
)

type PackageManager interface {
	PackageStatus(ctx context.Context, name string) (PackageStatus, error)
	InstallPackages(ctx context.Context, names []string) error
	UpgradePackages(ctx context.Context, names []string) error
	MarkUserPackages(ctx context.Context, names []string) error
	RemovePackages(ctx context.Context, names []string, autoremove bool) error
}

type packageManagerWithStatusOptions interface {
	PackageStatusWithOptions(ctx context.Context, name string, includeVersions bool) (PackageStatus, error)
}

type PackageStatus struct {
	Name             string
	Installed        bool
	ReasonUser       bool
	InstallReason    string
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

	cacheMu           sync.RWMutex
	cacheGeneration   uint64
	localSnapshot     map[string]PackageStatus
	versionStatuses   map[string]dnfVersionStatus
	localSnapshotLoad singleflight.Group
	versionStatusLoad singleflight.Group
}

type dnfVersionStatus struct {
	candidateVersion string
	upgradeVersion   string
}

// DNF and RPM use an exclusive transaction lock. Terraform may apply
// independent package resources concurrently, so mutating commands must be
// serialized before DNF has a chance to fail on its own lock.
var dnfMutateMutex sync.Mutex

func NewCLIPackageManager(dnfPath string, sudoPath string) *CLIPackageManager {
	return &CLIPackageManager{
		dnfPath:         dnfPath,
		sudoPath:        sudoPath,
		versionStatuses: make(map[string]dnfVersionStatus),
	}
}

func (m *CLIPackageManager) PackageStatus(ctx context.Context, name string) (PackageStatus, error) {
	return m.PackageStatusWithOptions(ctx, name, true)
}

// PackageStatusWithOptions shares one installed-package snapshot across DNF
// resources. Candidate and upgrade queries are cached per package and are only
// run when the caller manages versions.
func (m *CLIPackageManager) PackageStatusWithOptions(ctx context.Context, name string, includeVersions bool) (PackageStatus, error) {
	if err := validatePackageName(name); err != nil {
		return PackageStatus{}, err
	}

	status, err := m.localPackageStatus(ctx, name)
	if err != nil {
		return PackageStatus{}, err
	}
	if !includeVersions {
		return status, nil
	}

	versions, err := m.packageVersionStatus(ctx, name)
	if err != nil {
		return PackageStatus{}, err
	}
	status.CandidateVersion = versions.candidateVersion
	status.UpgradeVersion = versions.upgradeVersion
	return status, nil
}

func (m *CLIPackageManager) localPackageStatus(ctx context.Context, name string) (PackageStatus, error) {
	snapshot, err := m.getLocalSnapshot(ctx)
	if err != nil {
		return PackageStatus{}, err
	}
	status, ok := snapshot[name]
	if !ok {
		return PackageStatus{Name: name}, nil
	}
	status.Name = name
	return status, nil
}

func (m *CLIPackageManager) getLocalSnapshot(ctx context.Context) (map[string]PackageStatus, error) {
	m.cacheMu.RLock()
	generation := m.cacheGeneration
	if m.localSnapshot != nil {
		snapshot := m.localSnapshot
		m.cacheMu.RUnlock()
		return snapshot, nil
	}
	m.cacheMu.RUnlock()

	key := strconv.FormatUint(generation, 10)
	value, err, _ := m.localSnapshotLoad.Do(key, func() (any, error) {
		dnfMutateMutex.Lock()
		defer dnfMutateMutex.Unlock()

		out, err := m.run(
			ctx,
			false,
			m.dnfPath,
			"-q",
			"repoquery",
			"--installed",
			"--queryformat",
			"%{name}\t%{arch}\t%{evr}\t%{reason}\n",
		)
		if err != nil {
			return nil, err
		}
		snapshot := parseDNFInstalledSnapshot(out)

		m.cacheMu.Lock()
		if m.cacheGeneration == generation {
			m.localSnapshot = snapshot
		}
		m.cacheMu.Unlock()
		return snapshot, nil
	})
	if err != nil {
		return nil, err
	}
	snapshot, ok := value.(map[string]PackageStatus)
	if !ok {
		return nil, fmt.Errorf("unexpected DNF snapshot result %T", value)
	}
	return snapshot, nil
}

func (m *CLIPackageManager) packageVersionStatus(ctx context.Context, name string) (dnfVersionStatus, error) {
	m.cacheMu.RLock()
	generation := m.cacheGeneration
	if status, ok := m.versionStatuses[name]; ok {
		m.cacheMu.RUnlock()
		return status, nil
	}
	m.cacheMu.RUnlock()

	key := strconv.FormatUint(generation, 10) + ":" + name
	value, err, _ := m.versionStatusLoad.Do(key, func() (any, error) {
		dnfMutateMutex.Lock()
		defer dnfMutateMutex.Unlock()

		status := dnfVersionStatus{}
		candidateOut, err := m.run(
			ctx,
			false,
			m.dnfPath,
			"-q",
			"repoquery",
			"--latest-limit=1",
			"--queryformat",
			"%{name}\t%{arch}\t%{evr}\n",
			name,
		)
		if err != nil {
			return dnfVersionStatus{}, err
		}
		status.candidateVersion = parseDNFVersionQuery(candidateOut, name)

		upgradeOut, err := m.run(
			ctx,
			false,
			m.dnfPath,
			"-q",
			"repoquery",
			"--upgrades",
			"--latest-limit=1",
			"--queryformat",
			"%{name}\t%{arch}\t%{evr}\n",
			name,
		)
		if err != nil {
			return dnfVersionStatus{}, err
		}
		status.upgradeVersion = parseDNFVersionQuery(upgradeOut, name)

		m.cacheMu.Lock()
		if m.cacheGeneration == generation {
			if m.versionStatuses == nil {
				m.versionStatuses = make(map[string]dnfVersionStatus)
			}
			m.versionStatuses[name] = status
		}
		m.cacheMu.Unlock()
		return status, nil
	})
	if err != nil {
		return dnfVersionStatus{}, err
	}
	status, ok := value.(dnfVersionStatus)
	if !ok {
		return dnfVersionStatus{}, fmt.Errorf("unexpected DNF version result %T", value)
	}
	return status, nil
}

func parseDNFInstalledSnapshot(out []byte) map[string]PackageStatus {
	snapshot := make(map[string]PackageStatus)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 4 {
			continue
		}
		packageName := strings.TrimSpace(parts[0])
		architecture := strings.TrimSpace(parts[1])
		version := strings.TrimSpace(parts[2])
		reason := strings.ToLower(strings.TrimSpace(parts[3]))
		if packageName == "" || architecture == "" || version == "" {
			continue
		}
		status := PackageStatus{
			Name:             packageName,
			Installed:        true,
			ReasonUser:       reason == dnfPackageInstallReasonUser,
			InstallReason:    reason,
			InstalledVersion: version,
		}
		if _, exists := snapshot[packageName]; !exists {
			snapshot[packageName] = status
		}
		snapshot[packageName+"."+architecture] = status
	}
	return snapshot
}

func parseDNFVersionQuery(out []byte, requestedName string) string {
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\t")
		if len(parts) != 3 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		architecture := strings.TrimSpace(parts[1])
		if requestedName != name && requestedName != name+"."+architecture {
			continue
		}
		return strings.TrimSpace(parts[2])
	}
	return ""
}

func (m *CLIPackageManager) invalidateStatusCache() {
	m.cacheMu.Lock()
	m.cacheGeneration++
	m.localSnapshot = nil
	m.versionStatuses = make(map[string]dnfVersionStatus)
	m.cacheMu.Unlock()
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
	var mutationLock *lockedHostFile
	if mutate {
		dnfMutateMutex.Lock()
		defer dnfMutateMutex.Unlock()

		var err error
		mutationLock, err = lockHostFileContext(ctx, "dnf:"+m.dnfPath)
		if err != nil {
			return nil, err
		}
		defer mutationLock.close()
		defer m.invalidateStatusCache()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCommandWithExecutableBusyRetry(ctx, func() *exec.Cmd {
		stdout.Reset()
		stderr.Reset()
		cmd := exec.CommandContext(ctx, commandName, commandArgs...)
		cmd.Env = environmentWithCLocale(cmd.Environ())
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		return cmd
	})
	if err != nil {
		return nil, fmt.Errorf("%s %s failed: %w\n%s", commandName, strings.Join(commandArgs, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

func (m *CLIPackageManager) authenticateSudo(ctx context.Context, name string, args ...string) error {
	if err := runCommandWithExecutableBusyRetry(ctx, func() *exec.Cmd {
		return exec.CommandContext(ctx, m.sudoPath, "-n", "-v")
	}); err == nil {
		return nil
	}

	fmt.Fprintf(os.Stderr, "\nTerraform provider host needs sudo privileges for: %s %s\n", name, strings.Join(args, " "))
	fmt.Fprintln(os.Stderr, "Enter your sudo password at the prompt below, or run `sudo -v` before `terraform apply`.")

	if err := runCommandWithExecutableBusyRetry(ctx, func() *exec.Cmd {
		cmd := exec.CommandContext(ctx, m.sudoPath, "-v")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd
	}); err != nil {
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
