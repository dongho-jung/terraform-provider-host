package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"
)

// pacmanMutateMutex serializes mutating pacman invocations across the whole
// provider process. pacman takes an exclusive database lock (db.lck), so
// concurrent -S/-D/-R calls (which Terraform issues in parallel for
// independent resources) would otherwise fail with "unable to lock database".
// Read-only queries do not take the lock and are left unserialized.
var pacmanMutateMutex sync.Mutex

type CLIPacmanPackageManager struct {
	pacmanPath string
	sudoPath   string

	cacheMu           sync.RWMutex
	cacheGeneration   uint64
	localSnapshot     *pacmanLocalSnapshot
	versionStatuses   map[string]pacmanVersionStatus
	localSnapshotLoad singleflight.Group
	versionStatusLoad singleflight.Group

	aurHelperMu       sync.RWMutex
	verifiedAURHelper map[string]verifiedAURHelper
}

type pacmanLocalSnapshot struct {
	installedVersions map[string]string
	explicitPackages  map[string]struct{}
}

type pacmanVersionStatus struct {
	candidateVersion string
	upgradeVersion   string
}

func NewCLIPacmanPackageManager(pacmanPath string, sudoPath string) *CLIPacmanPackageManager {
	return &CLIPacmanPackageManager{
		pacmanPath:        pacmanPath,
		sudoPath:          sudoPath,
		versionStatuses:   make(map[string]pacmanVersionStatus),
		verifiedAURHelper: make(map[string]verifiedAURHelper),
	}
}

// PackageOwner reports the package owning path according to Pacman's local
// database. The quiet owner query is the locale-independent form of -Qo.
func (m *CLIPacmanPackageManager) PackageOwner(ctx context.Context, path string) (string, bool, error) {
	out, found, err := m.runOptional(ctx, m.pacmanPath, "-Qqo", "--", path)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}

	owners := parsePackageNames(out)
	if len(owners) != 1 {
		return "", false, fmt.Errorf("expected one Pacman owner for %q, got %q", path, strings.TrimSpace(string(out)))
	}
	return owners[0], true, nil
}

func (m *CLIPacmanPackageManager) PackageStatus(ctx context.Context, name string) (PackageStatus, error) {
	return m.PackageStatusWithOptions(ctx, name, true)
}

// PackageStatusWithOptions reads package state from a shared local snapshot.
// Candidate and upgrade queries are only run when includeVersions is true.
func (m *CLIPacmanPackageManager) PackageStatusWithOptions(ctx context.Context, name string, includeVersions bool) (PackageStatus, error) {
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

func (m *CLIPacmanPackageManager) localPackageStatus(ctx context.Context, name string) (PackageStatus, error) {
	snapshot, err := m.getLocalSnapshot(ctx)
	if err != nil {
		return PackageStatus{}, err
	}

	status := PackageStatus{Name: name}
	if version, ok := snapshot.installedVersions[name]; ok {
		status.Installed = true
		status.InstalledVersion = version
	}
	_, status.ReasonUser = snapshot.explicitPackages[name]
	return status, nil
}

func (m *CLIPacmanPackageManager) getLocalSnapshot(ctx context.Context) (*pacmanLocalSnapshot, error) {
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
		pacmanMutateMutex.Lock()
		defer pacmanMutateMutex.Unlock()

		installedOut, err := m.run(ctx, false, m.pacmanPath, "-Q")
		if err != nil {
			return nil, err
		}
		explicitOut, err := m.run(ctx, false, m.pacmanPath, "-Qqe")
		if err != nil {
			return nil, err
		}

		snapshot := &pacmanLocalSnapshot{
			installedVersions: parsePacmanInstalledVersions(installedOut),
			explicitPackages:  packageNameSet(explicitOut),
		}

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
	snapshot, ok := value.(*pacmanLocalSnapshot)
	if !ok {
		return nil, fmt.Errorf("unexpected Pacman snapshot result %T", value)
	}
	return snapshot, nil
}

func (m *CLIPacmanPackageManager) packageVersionStatus(ctx context.Context, name string) (pacmanVersionStatus, error) {
	m.cacheMu.RLock()
	generation := m.cacheGeneration
	if status, ok := m.versionStatuses[name]; ok {
		m.cacheMu.RUnlock()
		return status, nil
	}
	m.cacheMu.RUnlock()

	key := strconv.FormatUint(generation, 10) + ":" + name
	value, err, _ := m.versionStatusLoad.Do(key, func() (any, error) {
		status := pacmanVersionStatus{}
		candidateOut, candidateFound, err := m.runOptional(ctx, m.pacmanPath, "-Si", name)
		if err != nil {
			return pacmanVersionStatus{}, err
		}
		if candidateFound {
			status.candidateVersion = parsePacmanInfoValue(string(candidateOut), "Version")
		}

		upgradeOut, upgradeFound, err := m.runOptional(ctx, m.pacmanPath, "-Qu", name)
		if err != nil {
			return pacmanVersionStatus{}, err
		}
		if upgradeFound {
			packageName, version, ok := parsePacmanUpgradeLine(strings.TrimSpace(string(upgradeOut)))
			if ok && packageName == name {
				status.upgradeVersion = version
			}
		}

		m.cacheMu.Lock()
		if m.cacheGeneration == generation {
			if m.versionStatuses == nil {
				m.versionStatuses = make(map[string]pacmanVersionStatus)
			}
			m.versionStatuses[name] = status
		}
		m.cacheMu.Unlock()
		return status, nil
	})
	if err != nil {
		return pacmanVersionStatus{}, err
	}
	status, ok := value.(pacmanVersionStatus)
	if !ok {
		return pacmanVersionStatus{}, fmt.Errorf("unexpected Pacman version status result %T", value)
	}
	return status, nil
}

func (m *CLIPacmanPackageManager) invalidateStatusCache() {
	m.cacheMu.Lock()
	m.cacheGeneration++
	m.localSnapshot = nil
	m.versionStatuses = make(map[string]pacmanVersionStatus)
	m.cacheMu.Unlock()
}

func parsePacmanInstalledVersions(out []byte) map[string]string {
	versions := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		name, version, ok := parsePacmanPackageLine(strings.TrimSpace(line))
		if ok {
			versions[name] = version
		}
	}
	return versions
}

func packageNameSet(out []byte) map[string]struct{} {
	set := make(map[string]struct{})
	for _, name := range parsePackageNames(out) {
		set[name] = struct{}{}
	}
	return set
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

// runOptional runs a read-only query and reports "no result" instead of an
// error when the package is simply unknown.
func (m *CLIPacmanPackageManager) runOptional(ctx context.Context, name string, args ...string) ([]byte, bool, error) {
	out, err := m.run(ctx, false, name, args...)
	if err == nil {
		return out, true, nil
	}
	if isPacmanNoResultError(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func (m *CLIPacmanPackageManager) run(ctx context.Context, mutate bool, name string, args ...string) ([]byte, error) {
	if mutate {
		pacmanMutateMutex.Lock()
		defer pacmanMutateMutex.Unlock()
		defer m.invalidateStatusCache()
	}

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
			strings.Contains(text, "no package owns") ||
			strings.Contains(text, "target not found") ||
			strings.TrimSpace(strings.Split(text, "\n")[len(strings.Split(text, "\n"))-1]) == "")
}
