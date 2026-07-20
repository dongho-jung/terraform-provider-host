package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestValidateAURHelperSpec(t *testing.T) {
	t.Parallel()

	for _, spec := range []AURHelperSpec{
		{Name: "yay", Package: "yay"},
		{Name: "yay", Package: "yay-bin"},
		{Name: "paru", Package: "paru-bin"},
	} {
		if err := validateAURHelperSpec(spec); err != nil {
			t.Fatalf("validate %#v: %s", spec, err)
		}
	}

	for _, spec := range []AURHelperSpec{
		{Name: "", Package: "yay"},
		{Name: "pacaur", Package: "pacaur"},
		{Name: "yay", Package: "-invalid"},
	} {
		if err := validateAURHelperSpec(spec); err == nil {
			t.Fatalf("expected %#v to be rejected", spec)
		}
	}
}

func TestCLIAURHelperManagerAdoptsInstalledHelperAndMarksItExplicit(t *testing.T) {
	pacmanPath := writeMockPacman(t)
	if err := os.WriteFile(pacmanPath+".helper-installed", nil, 0o600); err != nil {
		t.Fatalf("mark helper installed: %s", err)
	}
	if err := os.WriteFile(pacmanPath+".owner", []byte("yay-bin\n"), 0o600); err != nil {
		t.Fatalf("write helper owner: %s", err)
	}
	pacman := NewCLIPacmanPackageManager(pacmanPath, pacmanPath)
	manager := NewCLIAURHelperManager(pacman)

	helperDir := t.TempDir()
	helperPath := filepath.Join(helperDir, "yay")
	if err := os.WriteFile(helperPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write helper: %s", err)
	}
	t.Setenv("PATH", helperDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	status, err := manager.EnsureHelper(t.Context(), AURHelperSpec{Name: "yay", Package: "yay-bin"})
	if err != nil {
		t.Fatalf("ensure helper: %s", err)
	}
	if status.Name != "yay" || status.Package != "yay-bin" || status.Path != helperPath {
		t.Fatalf("unexpected helper status: %#v", status)
	}

	packageStatus, err := pacman.PackageStatusWithOptions(t.Context(), "yay-bin", false)
	if err != nil {
		t.Fatalf("read package status: %s", err)
	}
	if !packageStatus.ReasonUser {
		t.Fatal("expected helper package to be marked explicit")
	}
}

func TestCLIAURHelperManagerRejectsExecutableOwnedByAnotherPackage(t *testing.T) {
	pacmanPath := writeMockPacman(t)
	if err := os.WriteFile(pacmanPath+".owner", []byte("yay-bin\n"), 0o600); err != nil {
		t.Fatalf("write helper owner: %s", err)
	}
	pacman := NewCLIPacmanPackageManager(pacmanPath, pacmanPath)
	manager := NewCLIAURHelperManager(pacman)

	helperDir := t.TempDir()
	helperPath := filepath.Join(helperDir, "yay")
	if err := os.WriteFile(helperPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write helper: %s", err)
	}
	t.Setenv("PATH", helperDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := manager.EnsureHelper(t.Context(), AURHelperSpec{Name: "yay", Package: "glib2"})
	if err == nil || !strings.Contains(err.Error(), "owned by package \"yay-bin\"") {
		t.Fatalf("expected package ownership mismatch, got %v", err)
	}
	if counts := mockPacmanCommandCounts(t, pacmanPath); counts["-D"] != 0 || counts["-R"] != 0 {
		t.Fatalf("ownership mismatch must not mutate packages: %#v", counts)
	}
}

func TestCLIAURHelperManagerBootstrapsOnceWithControlledPacmanAuth(t *testing.T) {
	pacmanPath := writeMockPacman(t)
	if err := os.WriteFile(pacmanPath+".owner", []byte("yay-bin\n"), 0o600); err != nil {
		t.Fatalf("write helper owner: %s", err)
	}

	toolsDir := t.TempDir()
	helperPath := filepath.Join(toolsDir, "yay")
	configCopyPath := filepath.Join(toolsDir, "used-makepkg.conf")
	sudoLogPath := filepath.Join(toolsDir, "sudo.log")
	makepkgLogPath := filepath.Join(toolsDir, "makepkg.log")
	systemConfigPath := filepath.Join(toolsDir, "system-makepkg.conf")
	if err := os.WriteFile(systemConfigPath, []byte("CARCH=x86_64\n"), 0o600); err != nil {
		t.Fatalf("write system makepkg config: %s", err)
	}

	writeExecutable(t, toolsDir, "git", `#!/bin/sh
for destination in "$@"; do :; done
mkdir -p "$destination"
`)
	sudoPath := writeExecutable(t, toolsDir, "sudo", `#!/bin/sh
printf '%s\n' "$*" >> "$MOCK_SUDO_LOG"
if [ "$1" = "-n" ] && { [ "$2" = "true" ] || [ "$2" = "-v" ]; }; then
  exit 0
fi
if [ "$1" = "-v" ]; then
  exit 0
fi
exec "$@"
`)
	writeExecutable(t, toolsDir, "makepkg", `#!/usr/bin/bash
printf '%s\n' "$*" >> "$MOCK_MAKEPKG_LOG"
while (( $# > 0 )); do
  if [[ $1 = --config ]]; then
    config_path=$2
    shift 2
    continue
  fi
  shift
done
cp "$config_path" "$MOCK_CONFIG_COPY"
source "$config_path"
"${PACMAN_AUTH[@]}" true
sleep 0.05
printf '%s\n' '#!/bin/sh' 'exit 0' > "$MOCK_HELPER_PATH"
chmod 700 "$MOCK_HELPER_PATH"
: > "${MOCK_PACMAN_PATH}.helper-installed"
`)

	t.Setenv("PATH", toolsDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MOCK_PACMAN_PATH", pacmanPath)
	t.Setenv("MOCK_HELPER_PATH", helperPath)
	t.Setenv("MOCK_CONFIG_COPY", configCopyPath)
	t.Setenv("MOCK_SUDO_LOG", sudoLogPath)
	t.Setenv("MOCK_MAKEPKG_LOG", makepkgLogPath)

	pacman := NewCLIPacmanPackageManager(pacmanPath, sudoPath)
	manager := NewCLIAURHelperManager(pacman)
	manager.effectiveUID = func() int { return 1000 }
	manager.systemMakepkgConfigPath = systemConfigPath

	const callers = 8
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			status, err := manager.EnsureHelper(t.Context(), AURHelperSpec{Name: "yay", Package: "yay-bin"})
			if err == nil && (status.Path != helperPath || !status.ReasonUser) {
				err = fmt.Errorf("unexpected helper status: %#v", status)
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ensure helper: %s", err)
		}
	}

	makepkgLog, err := os.ReadFile(makepkgLogPath)
	if err != nil {
		t.Fatalf("read makepkg log: %s", err)
	}
	if lines := splitNonEmptyLines(string(makepkgLog)); len(lines) != 1 {
		t.Fatalf("makepkg ran %d times, want once: %q", len(lines), makepkgLog)
	}
	controlledConfig, err := os.ReadFile(configCopyPath)
	if err != nil {
		t.Fatalf("read controlled makepkg config: %s", err)
	}
	if !strings.Contains(string(controlledConfig), "PACMAN_AUTH=("+shellSingleQuote(sudoPath)+" '-n')") {
		t.Fatalf("controlled PACMAN_AUTH missing from config:\n%s", controlledConfig)
	}
	if strings.Contains(string(controlledConfig), "-k") {
		t.Fatalf("controlled config must not invalidate sudo credentials:\n%s", controlledConfig)
	}
	sudoLog, err := os.ReadFile(sudoLogPath)
	if err != nil {
		t.Fatalf("read sudo log: %s", err)
	}
	if strings.Contains(string(sudoLog), "-k") {
		t.Fatalf("sudo -k must not be invoked: %s", sudoLog)
	}
}

func TestCLIAURHelperManagerMissingSudoFailsBeforeBootstrap(t *testing.T) {
	pacmanPath := writeMockPacman(t)
	pacman := NewCLIPacmanPackageManager(pacmanPath, "")
	manager := NewCLIAURHelperManager(pacman)
	manager.effectiveUID = func() int { return 1000 }

	_, err := manager.EnsureHelper(t.Context(), AURHelperSpec{Name: "yay", Package: "yay-bin"})
	if err == nil || !strings.Contains(err.Error(), "sudo was not found") {
		t.Fatalf("expected missing sudo diagnostic, got %v", err)
	}
}

func TestResolvingAURPackageManagerReadsLocalStatusWithoutHelper(t *testing.T) {
	t.Parallel()

	pacmanPath := writeMockPacman(t)
	pacman := NewCLIPacmanPackageManager(pacmanPath, pacmanPath)
	manager := NewResolvingAURPackageManager(pacman)
	manager.helperNames = []string{"terraform-provider-host-test-missing-helper"}

	status, err := manager.PackageStatus(t.Context(), "playerctl", false)
	if err != nil {
		t.Fatalf("read local status: %s", err)
	}
	if !status.Installed || !status.ReasonUser {
		t.Fatalf("unexpected local status: %#v", status)
	}

	remoteStatus, err := manager.PackageStatus(t.Context(), "playerctl", true)
	if err == nil || !errors.Is(err, errAURHelperUnavailable) {
		t.Fatal("expected a remote lookup to require an AUR helper")
	}
	if !remoteStatus.Installed || !remoteStatus.ReasonUser {
		t.Fatalf("remote-unavailable error should retain local status: %#v", remoteStatus)
	}
	if err := manager.InstallPackages(t.Context(), []string{"wl-kbptr"}); err == nil {
		t.Fatal("apply installation must strictly require an AUR helper")
	}
}

func TestResolvingAURPackageManagerUsesVerifiedManagedHelper(t *testing.T) {
	pacmanPath := writeMockPacman(t)
	if err := os.WriteFile(pacmanPath+".helper-installed", nil, 0o600); err != nil {
		t.Fatalf("mark helper installed: %s", err)
	}
	if err := os.WriteFile(pacmanPath+".owner", []byte("yay-bin\n"), 0o600); err != nil {
		t.Fatalf("write helper owner: %s", err)
	}
	pacman := NewCLIPacmanPackageManager(pacmanPath, pacmanPath)

	helperDir := t.TempDir()
	helperPath := writeExecutable(t, helperDir, "yay", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", helperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	helperManager := NewCLIAURHelperManager(pacman)
	if _, exists, err := helperManager.HelperStatus(t.Context(), AURHelperSpec{Name: "yay", Package: "yay-bin"}); err != nil || !exists {
		t.Fatalf("register verified helper: exists=%t err=%v", exists, err)
	}

	resolver := NewResolvingAURPackageManager(pacman)
	resolver.helperNames = []string{"yay"}
	resolved, err := resolver.resolve(t.Context())
	if err != nil {
		t.Fatalf("resolve verified helper: %s", err)
	}
	if resolved.helperPath != helperPath {
		t.Fatalf("resolved path %q, want %q", resolved.helperPath, helperPath)
	}
}

func TestResolvingAURPackageManagerRejectsUnrelatedPATHExecutable(t *testing.T) {
	pacmanPath := writeMockPacman(t)
	if err := os.WriteFile(pacmanPath+".owner", []byte("glib2\n"), 0o600); err != nil {
		t.Fatalf("write unrelated owner: %s", err)
	}
	pacman := NewCLIPacmanPackageManager(pacmanPath, pacmanPath)
	helperDir := t.TempDir()
	writeExecutable(t, helperDir, "yay", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", helperDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	resolver := NewResolvingAURPackageManager(pacman)
	resolver.helperNames = []string{"yay"}
	if _, err := resolver.resolve(t.Context()); err == nil {
		t.Fatal("expected unrelated PATH executable to be rejected")
	}
}

func writeExecutable(t *testing.T, dir string, name string, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write executable %s: %s", name, err)
	}
	return path
}
