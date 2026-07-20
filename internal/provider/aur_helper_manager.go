package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sync/singleflight"
)

type AURHelperManager interface {
	HelperStatus(ctx context.Context, spec AURHelperSpec) (AURHelperStatus, bool, error)
	EnsureHelper(ctx context.Context, spec AURHelperSpec) (AURHelperStatus, error)
	RemoveHelper(ctx context.Context, spec AURHelperSpec) error
	NeedsPrivilegeEscalation() bool
}

type AURHelperSpec struct {
	Name    string
	Package string
}

type AURHelperStatus struct {
	Name             string
	Package          string
	Path             string
	InstalledVersion string
	ReasonUser       bool
}

type CLIAURHelperManager struct {
	pacman                  *CLIPacmanPackageManager
	lookPath                func(string) (string, error)
	effectiveUID            func() int
	systemMakepkgConfigPath string
	ensureGroup             singleflight.Group
}

func NewCLIAURHelperManager(pacman *CLIPacmanPackageManager) *CLIAURHelperManager {
	return &CLIAURHelperManager{
		pacman:                  pacman,
		lookPath:                exec.LookPath,
		effectiveUID:            os.Geteuid,
		systemMakepkgConfigPath: "/etc/makepkg.conf",
	}
}

type verifiedAURHelper struct {
	Name    string
	Package string
	Path    string
}

func (m *CLIAURHelperManager) HelperStatus(ctx context.Context, spec AURHelperSpec) (AURHelperStatus, bool, error) {
	if err := validateAURHelperSpec(spec); err != nil {
		return AURHelperStatus{}, false, err
	}

	packageStatus, err := m.pacman.localPackageStatus(ctx, spec.Package)
	if err != nil {
		return AURHelperStatus{}, false, err
	}
	if !packageStatus.Installed {
		m.pacman.forgetVerifiedAURHelper(spec)
		return AURHelperStatus{}, false, nil
	}

	helperPath, err := m.lookupPath(spec.Name)
	if err != nil {
		return AURHelperStatus{}, false, fmt.Errorf("package %q is installed but its %q executable was not found in PATH", spec.Package, spec.Name)
	}
	helperPath, err = canonicalExecutablePath(helperPath)
	if err != nil {
		return AURHelperStatus{}, false, fmt.Errorf("resolve AUR helper executable %q: %w", helperPath, err)
	}
	owner, owned, err := m.pacman.PackageOwner(ctx, helperPath)
	if err != nil {
		return AURHelperStatus{}, false, fmt.Errorf("read owner of AUR helper executable %q: %w", helperPath, err)
	}
	if !owned {
		return AURHelperStatus{}, false, fmt.Errorf("AUR helper executable %q is not owned by a Pacman package", helperPath)
	}
	if owner != spec.Package {
		return AURHelperStatus{}, false, fmt.Errorf("AUR helper executable %q is owned by package %q, not configured package %q", helperPath, owner, spec.Package)
	}
	m.pacman.rememberVerifiedAURHelper(verifiedAURHelper{Name: spec.Name, Package: spec.Package, Path: helperPath})

	return AURHelperStatus{
		Name:             spec.Name,
		Package:          spec.Package,
		Path:             helperPath,
		InstalledVersion: packageStatus.InstalledVersion,
		ReasonUser:       packageStatus.ReasonUser,
	}, true, nil
}

func (m *CLIAURHelperManager) EnsureHelper(ctx context.Context, spec AURHelperSpec) (AURHelperStatus, error) {
	if err := validateAURHelperSpec(spec); err != nil {
		return AURHelperStatus{}, err
	}

	result := m.ensureGroup.DoChan(spec.Name+"\x00"+spec.Package, func() (any, error) {
		return m.ensureHelper(ctx, spec)
	})
	select {
	case <-ctx.Done():
		return AURHelperStatus{}, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return AURHelperStatus{}, result.Err
		}
		status, ok := result.Val.(AURHelperStatus)
		if !ok {
			return AURHelperStatus{}, fmt.Errorf("unexpected AUR helper bootstrap result %T", result.Val)
		}
		return status, nil
	}
}

func (m *CLIAURHelperManager) ensureHelper(ctx context.Context, spec AURHelperSpec) (AURHelperStatus, error) {
	status, exists, err := m.HelperStatus(ctx, spec)
	if err != nil {
		return AURHelperStatus{}, err
	}
	if exists {
		if !status.ReasonUser {
			if err := m.pacman.MarkUserPackages(ctx, []string{spec.Package}); err != nil {
				return AURHelperStatus{}, err
			}
			return m.requireHelperStatus(ctx, spec)
		}
		return status, nil
	}

	if m.getEffectiveUID() == 0 {
		return AURHelperStatus{}, fmt.Errorf("AUR packages cannot be built as root; run Terraform as the unprivileged target user")
	}
	if m.pacman.sudoPath == "" {
		return AURHelperStatus{}, fmt.Errorf("bootstrap AUR helper %q: sudo was not found in PATH; non-root makepkg installation requires sudo", spec.Name)
	}

	gitPath, err := m.lookupPath("git")
	if err != nil {
		return AURHelperStatus{}, fmt.Errorf("bootstrap AUR helper %q: git was not found in PATH; install the git package first", spec.Name)
	}
	makepkgPath, err := m.lookupPath("makepkg")
	if err != nil {
		return AURHelperStatus{}, fmt.Errorf("bootstrap AUR helper %q: makepkg was not found in PATH; install the base-devel package first", spec.Name)
	}

	buildRoot, err := os.MkdirTemp("", "terraform-provider-host-aur-helper-*")
	if err != nil {
		return AURHelperStatus{}, fmt.Errorf("create AUR helper build directory: %w", err)
	}
	defer os.RemoveAll(buildRoot)

	repositoryPath := filepath.Join(buildRoot, spec.Package)
	repositoryURL := "https://aur.archlinux.org/" + spec.Package + ".git"
	if err := runAURHelperBootstrapCommand(ctx, "", gitPath, "clone", "--depth=1", "--", repositoryURL, repositoryPath); err != nil {
		return AURHelperStatus{}, err
	}

	sudoPath, err := canonicalExecutablePath(m.pacman.sudoPath)
	if err != nil {
		return AURHelperStatus{}, fmt.Errorf("resolve sudo executable %q: %w", m.pacman.sudoPath, err)
	}
	makepkgConfigPath, err := writeControlledMakepkgConfig(buildRoot, m.systemMakepkgConfigPath, sudoPath)
	if err != nil {
		return AURHelperStatus{}, err
	}
	makepkgArgs := []string{"--config", makepkgConfigPath, "--syncdeps", "--install", "--needed", "--noconfirm"}
	if err := m.pacman.authenticateSudo(ctx, makepkgPath, makepkgArgs...); err != nil {
		return AURHelperStatus{}, err
	}

	err = func() error {
		pacmanMutateMutex.Lock()
		defer pacmanMutateMutex.Unlock()
		defer m.pacman.invalidateStatusCache()
		return runAURHelperBootstrapCommand(ctx, repositoryPath, makepkgPath, makepkgArgs...)
	}()
	if err != nil {
		return AURHelperStatus{}, err
	}

	if err := m.pacman.MarkUserPackages(ctx, []string{spec.Package}); err != nil {
		return AURHelperStatus{}, err
	}
	return m.requireHelperStatus(ctx, spec)
}

func (m *CLIAURHelperManager) RemoveHelper(ctx context.Context, spec AURHelperSpec) error {
	if err := validateAURHelperSpec(spec); err != nil {
		return err
	}
	_, exists, err := m.HelperStatus(ctx, spec)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := m.pacman.RemovePackages(ctx, []string{spec.Package}, false); err != nil {
		return err
	}
	m.pacman.forgetVerifiedAURHelper(spec)
	return nil
}

func (m *CLIAURHelperManager) NeedsPrivilegeEscalation() bool {
	return m.getEffectiveUID() != 0
}

func (m *CLIAURHelperManager) requireHelperStatus(ctx context.Context, spec AURHelperSpec) (AURHelperStatus, error) {
	status, exists, err := m.HelperStatus(ctx, spec)
	if err != nil {
		return AURHelperStatus{}, err
	}
	if !exists {
		return AURHelperStatus{}, fmt.Errorf("AUR helper %q was not found after installing package %q", spec.Name, spec.Package)
	}
	return status, nil
}

func (m *CLIAURHelperManager) lookupPath(name string) (string, error) {
	if m.lookPath == nil {
		return exec.LookPath(name)
	}
	return m.lookPath(name)
}

func (m *CLIAURHelperManager) getEffectiveUID() int {
	if m.effectiveUID == nil {
		return os.Geteuid()
	}
	return m.effectiveUID()
}

func canonicalExecutablePath(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolutePath)
}

func writeControlledMakepkgConfig(buildRoot string, systemConfigPath string, sudoPath string) (string, error) {
	if !filepath.IsAbs(sudoPath) {
		return "", fmt.Errorf("controlled makepkg sudo path must be absolute, got %q", sudoPath)
	}
	if systemConfigPath == "" {
		systemConfigPath = "/etc/makepkg.conf"
	}
	if _, err := os.Stat(systemConfigPath); err != nil {
		return "", fmt.Errorf("read system makepkg config %q: %w", systemConfigPath, err)
	}

	var config strings.Builder
	config.WriteString("source " + shellSingleQuote(systemConfigPath) + "\n")
	configDir := systemConfigPath + ".d"
	if entries, err := filepath.Glob(filepath.Join(configDir, "*.conf")); err == nil {
		for _, entry := range entries {
			config.WriteString("source " + shellSingleQuote(entry) + "\n")
		}
	}
	if userConfigPath := existingUserMakepkgConfig(); userConfigPath != "" {
		config.WriteString("source " + shellSingleQuote(userConfigPath) + "\n")
	}
	config.WriteString("PACMAN_AUTH=(" + shellSingleQuote(sudoPath) + " '-n')\n")

	configPath := filepath.Join(buildRoot, "makepkg.conf")
	if err := os.WriteFile(configPath, []byte(config.String()), 0o600); err != nil {
		return "", fmt.Errorf("write controlled makepkg config: %w", err)
	}
	return configPath, nil
}

func existingUserMakepkgConfig() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		if homeDir, err := os.UserHomeDir(); err == nil {
			configHome = filepath.Join(homeDir, ".config")
		}
	}
	if configHome != "" {
		path := filepath.Join(configHome, "pacman", "makepkg.conf")
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	if homeDir, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(homeDir, ".makepkg.conf")
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (m *CLIPacmanPackageManager) rememberVerifiedAURHelper(helper verifiedAURHelper) {
	m.aurHelperMu.Lock()
	if m.verifiedAURHelper == nil {
		m.verifiedAURHelper = make(map[string]verifiedAURHelper)
	}
	m.verifiedAURHelper[helper.Name] = helper
	m.aurHelperMu.Unlock()
}

func (m *CLIPacmanPackageManager) getVerifiedAURHelper(name string) (verifiedAURHelper, bool) {
	m.aurHelperMu.RLock()
	helper, ok := m.verifiedAURHelper[name]
	m.aurHelperMu.RUnlock()
	return helper, ok
}

func (m *CLIPacmanPackageManager) forgetVerifiedAURHelper(spec AURHelperSpec) {
	m.aurHelperMu.Lock()
	if helper, ok := m.verifiedAURHelper[spec.Name]; ok && helper.Package == spec.Package {
		delete(m.verifiedAURHelper, spec.Name)
	}
	m.aurHelperMu.Unlock()
}

func validateAURHelperSpec(spec AURHelperSpec) error {
	if err := validateAURHelperName(spec.Name); err != nil {
		return err
	}
	if err := validatePackageName(spec.Package); err != nil {
		return fmt.Errorf("invalid AUR helper package: %w", err)
	}
	return nil
}

func validateAURHelperName(name string) error {
	if name != "yay" && name != "paru" {
		return fmt.Errorf("AUR helper name must be either %q or %q", "yay", "paru")
	}
	return nil
}

func runAURHelperBootstrapCommand(ctx context.Context, dir string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		output := strings.TrimSpace(stderr.String())
		if output == "" {
			output = strings.TrimSpace(stdout.String())
		}
		return fmt.Errorf("%s %s failed: %w\n%s", name, strings.Join(args, " "), err, output)
	}
	return nil
}
