package provider

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParsePacmanPackageLine(t *testing.T) {
	t.Parallel()

	name, version, ok := parsePacmanPackageLine("git 2.50.1-1")
	if !ok {
		t.Fatal("expected package line to parse")
	}
	if name != "git" || version != "2.50.1-1" {
		t.Fatalf("got name=%q version=%q", name, version)
	}
}

func TestParsePacmanUpgradeLine(t *testing.T) {
	t.Parallel()

	name, version, ok := parsePacmanUpgradeLine("git 2.50.1-1 -> 2.51.0-1")
	if !ok {
		t.Fatal("expected upgrade line to parse")
	}
	if name != "git" || version != "2.51.0-1" {
		t.Fatalf("got name=%q version=%q", name, version)
	}
}

func TestParsePacmanInfoValue(t *testing.T) {
	t.Parallel()

	got := parsePacmanInfoValue("Repository      : extra\nName            : git\nVersion         : 2.50.1-1\n", "Version")
	if got != "2.50.1-1" {
		t.Fatalf("got %q", got)
	}
}

func TestParsePacmanInstalledVersions(t *testing.T) {
	t.Parallel()

	got := parsePacmanInstalledVersions([]byte("git 2.50.1-1\nglib2 2.84.4-1\nmalformed\n"))
	if len(got) != 2 || got["git"] != "2.50.1-1" || got["glib2"] != "2.84.4-1" {
		t.Fatalf("unexpected installed versions: %#v", got)
	}
}

func TestCLIPacmanPackageManagerSharesLocalSnapshotAndSkipsVersions(t *testing.T) {
	t.Parallel()

	pacmanPath := writeMockPacman(t)
	manager := NewCLIPacmanPackageManager(pacmanPath, pacmanPath)

	names := []string{"git", "glib2", "playerctl"}
	start := make(chan struct{})
	errs := make(chan error, len(names)*8)
	var wg sync.WaitGroup
	for range 8 {
		for _, name := range names {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := manager.PackageStatusWithOptions(t.Context(), name, false)
				errs <- err
			}()
		}
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("package status: %s", err)
		}
	}

	counts := mockPacmanCommandCounts(t, pacmanPath)
	if counts["-Q"] != 1 || counts["-Qqe"] != 1 {
		t.Fatalf("local query counts %#v, want one -Q and one -Qqe", counts)
	}
	if counts["-Si"] != 0 || counts["-Qu"] != 0 {
		t.Fatalf("version queries should be skipped, got %#v", counts)
	}
}

func TestCLIPacmanPackageManagerCachesVersionQueries(t *testing.T) {
	t.Parallel()

	pacmanPath := writeMockPacman(t)
	manager := NewCLIPacmanPackageManager(pacmanPath, pacmanPath)

	for range 2 {
		status, err := manager.PackageStatusWithOptions(t.Context(), "git", true)
		if err != nil {
			t.Fatalf("package status: %s", err)
		}
		if status.CandidateVersion != "2.51.0-1" || status.UpgradeVersion != "2.51.0-1" {
			t.Fatalf("unexpected version status: %#v", status)
		}
	}

	counts := mockPacmanCommandCounts(t, pacmanPath)
	for _, command := range []string{"-Q", "-Qqe", "-Si", "-Qu"} {
		if counts[command] != 1 {
			t.Fatalf("command counts %#v, want one %s", counts, command)
		}
	}
}

func TestCLIPacmanPackageManagerMutationInvalidatesSnapshot(t *testing.T) {
	t.Parallel()

	pacmanPath := writeMockPacman(t)
	manager := NewCLIPacmanPackageManager(pacmanPath, pacmanPath)

	before, err := manager.PackageStatusWithOptions(t.Context(), "glib2", false)
	if err != nil {
		t.Fatalf("package status before mutation: %s", err)
	}
	if before.ReasonUser {
		t.Fatal("expected glib2 to initially be a dependency")
	}

	if err := manager.MarkUserPackages(t.Context(), []string{"glib2"}); err != nil {
		t.Fatalf("mark explicit: %s", err)
	}

	after, err := manager.PackageStatusWithOptions(t.Context(), "glib2", false)
	if err != nil {
		t.Fatalf("package status after mutation: %s", err)
	}
	if !after.ReasonUser {
		t.Fatal("expected refreshed snapshot to observe explicit reason")
	}

	counts := mockPacmanCommandCounts(t, pacmanPath)
	if counts["-Q"] != 2 || counts["-Qqe"] != 2 || counts["-D"] != 1 {
		t.Fatalf("unexpected command counts after mutation: %#v", counts)
	}
}

func TestCLIAURPackageManagerSharesPacmanLocalSnapshot(t *testing.T) {
	t.Parallel()

	pacmanPath := writeMockPacman(t)
	pacman := NewCLIPacmanPackageManager(pacmanPath, pacmanPath)
	aur := NewCLIAURPackageManager("yay", pacmanPath, "", pacman)

	if _, err := pacman.PackageStatusWithOptions(t.Context(), "git", false); err != nil {
		t.Fatalf("pacman status: %s", err)
	}
	status, err := aur.PackageStatus(t.Context(), "playerctl", false)
	if err != nil {
		t.Fatalf("AUR status: %s", err)
	}
	if !status.Installed || !status.ReasonUser {
		t.Fatalf("unexpected AUR local status: %#v", status)
	}

	counts := mockPacmanCommandCounts(t, pacmanPath)
	if counts["-Q"] != 1 || counts["-Qqe"] != 1 {
		t.Fatalf("shared local query counts %#v, want one -Q and one -Qqe", counts)
	}
}

func writeMockPacman(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "pacman")
	contents := `#!/bin/sh
if [ "$1" = "$0" ]; then
  shift
fi
printf '%s\n' "$*" >> "${0}.log"
case "$1" in
  -n)
    exit 0
    ;;
  -Q)
    printf '%s\n' 'git 2.50.1-1' 'glib2 2.84.4-1' 'playerctl 2.4.1-4'
	if [ -f "${0}.helper-installed" ]; then
	  printf '%s\n' 'yay-bin 13.0.1-1'
	fi
    ;;
  -Qqe)
    printf '%s\n' 'git' 'playerctl'
    if [ -f "${0}.explicit" ]; then
	  cat "${0}.explicit"
    fi
    ;;
	-Qqo)
	  if [ -f "${0}.owner" ]; then
		cat "${0}.owner"
	  else
		printf '%s\n' "error: No package owns $3" >&2
		exit 1
	  fi
	  ;;
  -Si)
    printf '%s\n' 'Repository : extra' 'Name : git' 'Version : 2.51.0-1'
    ;;
  -Qu)
    printf '%s\n' 'git 2.50.1-1 -> 2.51.0-1'
    ;;
  -D)
	for package_name in "$@"; do :; done
	printf '%s\n' "$package_name" > "${0}.explicit"
    ;;
esac
`
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write mock pacman: %s", err)
	}
	return path
}

func mockPacmanCommandCounts(t *testing.T, pacmanPath string) map[string]int {
	t.Helper()

	contents, err := os.ReadFile(pacmanPath + ".log")
	if err != nil {
		t.Fatalf("read mock pacman log: %s", err)
	}
	counts := make(map[string]int)
	for _, line := range splitNonEmptyLines(string(contents)) {
		fields := splitFields(line)
		if len(fields) > 0 {
			counts[fields[0]]++
		}
	}
	return counts
}

func splitNonEmptyLines(value string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func splitFields(value string) []string {
	return strings.Fields(value)
}
