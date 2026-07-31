package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var errAURHelperUnavailable = errors.New("AUR helper unavailable")

// ResolvingAURPackageManager defers AUR helper discovery until an operation
// actually needs one. Mutations can optionally bootstrap a provider-configured
// helper, while read and planning paths remain free of installation side
// effects.
type ResolvingAURPackageManager struct {
	pacman          *CLIPacmanPackageManager
	helperNames     []string
	helperManager   AURHelperManager
	bootstrapHelper *AURHelperSpec
	options         AURPackageOptions
}

func NewResolvingAURPackageManager(pacman *CLIPacmanPackageManager, options ...AURPackageOptions) *ResolvingAURPackageManager {
	manager := &ResolvingAURPackageManager{
		pacman:      pacman,
		helperNames: []string{"yay", "paru"},
	}
	if len(options) > 0 {
		manager.options = options[0]
	}
	return manager
}

func NewBootstrappingAURPackageManager(pacman *CLIPacmanPackageManager, helperManager AURHelperManager, spec AURHelperSpec, options ...AURPackageOptions) *ResolvingAURPackageManager {
	manager := &ResolvingAURPackageManager{
		pacman:          pacman,
		helperNames:     []string{spec.Name},
		helperManager:   helperManager,
		bootstrapHelper: &spec,
	}
	if len(options) > 0 {
		manager.options = options[0]
	}
	return manager
}

func (m *ResolvingAURPackageManager) PackageStatus(ctx context.Context, name string, includeRemote bool) (PackageStatus, error) {
	if err := validatePackageName(name); err != nil {
		return PackageStatus{}, err
	}
	if !includeRemote {
		return m.pacman.localPackageStatus(ctx, name)
	}

	manager, err := m.resolve(ctx)
	if err != nil {
		localStatus, localErr := m.pacman.localPackageStatus(ctx, name)
		if localErr != nil {
			return PackageStatus{}, localErr
		}
		return localStatus, fmt.Errorf("%w: %v", errAURHelperUnavailable, err)
	}
	return manager.PackageStatus(ctx, name, true)
}

func (m *ResolvingAURPackageManager) InstallPackages(ctx context.Context, names []string) error {
	manager, err := m.resolveForMutation(ctx)
	if err != nil {
		return err
	}
	return manager.InstallPackages(ctx, names)
}

func (m *ResolvingAURPackageManager) UpgradePackages(ctx context.Context, names []string) error {
	manager, err := m.resolveForMutation(ctx)
	if err != nil {
		return err
	}
	return manager.UpgradePackages(ctx, names)
}

func (m *ResolvingAURPackageManager) MarkUserPackages(ctx context.Context, names []string) error {
	return m.pacman.MarkUserPackages(ctx, names)
}

func (m *ResolvingAURPackageManager) RemovePackages(ctx context.Context, names []string, autoremove bool) error {
	return m.pacman.RemovePackages(ctx, names, autoremove)
}

func (m *ResolvingAURPackageManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

func (m *ResolvingAURPackageManager) resolve(ctx context.Context) (*CLIAURPackageManager, error) {
	if m.bootstrapHelper != nil {
		if m.helperManager == nil {
			return nil, fmt.Errorf("AUR helper bootstrap manager is unavailable")
		}
		status, exists, err := m.helperManager.HelperStatus(ctx, *m.bootstrapHelper)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("configured AUR helper %q is not installed yet", m.bootstrapHelper.Name)
		}
		return NewCLIAURPackageManager(status.Name, status.Path, executablePath("vercmp"), m.pacman, m.options), nil
	}

	for _, helperName := range m.helperNames {
		if helper, ok := m.pacman.getVerifiedAURHelper(helperName); ok {
			path, verified, err := m.verifyHelperPath(ctx, helperName, helper.Package, helper.Path)
			if err != nil {
				return nil, err
			}
			if verified {
				return NewCLIAURPackageManager(helperName, path, executablePath("vercmp"), m.pacman, m.options), nil
			}
			m.pacman.forgetVerifiedAURHelper(AURHelperSpec{Name: helperName, Package: helper.Package})
		}

		helperPath, err := exec.LookPath(helperName)
		if err != nil {
			continue
		}
		helperPath, err = canonicalExecutablePath(helperPath)
		if err != nil {
			continue
		}
		owner, owned, err := m.pacman.PackageOwner(ctx, helperPath)
		if err != nil {
			return nil, err
		}
		if !owned || (owner != helperName && !strings.HasPrefix(owner, helperName+"-")) {
			continue
		}
		m.pacman.rememberVerifiedAURHelper(verifiedAURHelper{Name: helperName, Package: owner, Path: helperPath})
		return NewCLIAURPackageManager(helperName, helperPath, executablePath("vercmp"), m.pacman, m.options), nil
	}

	return nil, fmt.Errorf("verified AUR helper not found in PATH; configure aur_helper on the provider or install yay/paru")
}

func (m *ResolvingAURPackageManager) resolveForMutation(ctx context.Context) (*CLIAURPackageManager, error) {
	if m.bootstrapHelper == nil {
		return m.resolve(ctx)
	}
	if m.helperManager == nil {
		return nil, fmt.Errorf("AUR helper bootstrap manager is unavailable")
	}

	status, err := m.helperManager.EnsureHelper(ctx, *m.bootstrapHelper)
	if err != nil {
		return nil, err
	}
	return NewCLIAURPackageManager(status.Name, status.Path, executablePath("vercmp"), m.pacman, m.options), nil
}

func (m *ResolvingAURPackageManager) verifyHelperPath(ctx context.Context, name string, packageName string, path string) (string, bool, error) {
	canonicalPath, err := canonicalExecutablePath(path)
	if err == nil {
		owner, owned, ownerErr := m.pacman.PackageOwner(ctx, canonicalPath)
		if ownerErr != nil {
			return "", false, ownerErr
		}
		if owned && owner == packageName {
			m.pacman.rememberVerifiedAURHelper(verifiedAURHelper{Name: name, Package: packageName, Path: canonicalPath})
			return canonicalPath, true, nil
		}
	}
	return "", false, nil
}
