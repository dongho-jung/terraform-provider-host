package provider

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseBrewFormulaStatus(t *testing.T) {
	t.Parallel()

	status, err := parseBrewPackageStatus("bat", brewPackageTypeFormula, []byte(`{
  "formulae": [
    {
      "name": "bat",
      "full_name": "bat",
      "versions": { "stable": "0.26.1" },
      "installed": [
        { "version": "0.26.0", "installed_on_request": true }
      ],
      "linked_keg": "0.26.0",
      "outdated": true,
      "pinned": false
    }
  ],
  "casks": []
}`))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if status.Name != "bat" {
		t.Fatalf("expected name bat, got %q", status.Name)
	}
	if status.PackageType != brewPackageTypeFormula {
		t.Fatalf("expected formula package type, got %q", status.PackageType)
	}
	if !status.Installed {
		t.Fatal("expected installed formula")
	}
	if status.InstalledVersion != "0.26.0" {
		t.Fatalf("expected installed version 0.26.0, got %q", status.InstalledVersion)
	}
	if status.CandidateVersion != "0.26.1" {
		t.Fatalf("expected candidate version 0.26.1, got %q", status.CandidateVersion)
	}
	if status.UpgradeVersion != "0.26.1" {
		t.Fatalf("expected upgrade version 0.26.1, got %q", status.UpgradeVersion)
	}
	if !status.InstalledOnRequest {
		t.Fatal("expected formula to be installed on request")
	}
}

func TestParseBrewFormulaStatusNotInstalled(t *testing.T) {
	t.Parallel()

	status, err := parseBrewPackageStatus("git", brewPackageTypeFormula, []byte(`{
  "formulae": [
    {
      "name": "git",
      "full_name": "git",
      "versions": { "stable": "2.54.0" },
      "installed": [],
      "linked_keg": null,
      "outdated": false,
      "pinned": false
    }
  ],
  "casks": []
}`))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if status.Installed {
		t.Fatal("expected formula to be missing")
	}
	if status.CandidateVersion != "2.54.0" {
		t.Fatalf("expected candidate version 2.54.0, got %q", status.CandidateVersion)
	}
}

func TestParseBrewCaskStatus(t *testing.T) {
	t.Parallel()

	status, err := parseBrewPackageStatus("docker-desktop", brewPackageTypeCask, []byte(`{
  "formulae": [],
  "casks": [
    {
      "token": "docker-desktop",
      "full_token": "docker-desktop",
      "version": "4.79.0,230596",
      "installed": "4.71.0,225177",
      "outdated": true,
      "pinned": false,
      "auto_updates": true,
      "artifacts": [
        {
          "app": ["Docker.app"],
          "target": "/Applications/Docker.app"
        }
      ]
    }
  ]
}`))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if status.PackageType != brewPackageTypeCask {
		t.Fatalf("expected cask package type, got %q", status.PackageType)
	}
	if !status.Installed {
		t.Fatal("expected installed cask")
	}
	if status.InstalledVersion != "4.71.0,225177" {
		t.Fatalf("expected installed version, got %q", status.InstalledVersion)
	}
	if status.UpgradeVersion != "4.79.0,230596" {
		t.Fatalf("expected upgrade version, got %q", status.UpgradeVersion)
	}
	if !status.AutoUpdates {
		t.Fatal("expected auto-updating cask")
	}
	if len(status.AppPaths) != 1 || status.AppPaths[0] != "/Applications/Docker.app" {
		t.Fatalf("expected Docker app path, got %#v", status.AppPaths)
	}
}

func TestBrewCaskAppPathsIgnoresNonAppArtifacts(t *testing.T) {
	t.Parallel()

	status, err := parseBrewPackageStatus("hammerspoon", brewPackageTypeCask, []byte(`{
  "formulae": [],
  "casks": [
    {
      "token": "hammerspoon",
      "full_token": "hammerspoon",
      "version": "1.1.1",
      "installed": "1.1.1",
      "outdated": false,
      "pinned": false,
      "auto_updates": false,
      "artifacts": [
        { "uninstall": [{ "quit": "org.hammerspoon.Hammerspoon" }] },
        {
          "app": ["Hammerspoon.app"],
          "target": "/Applications/Hammerspoon.app"
        },
        {
          "binary": ["/Applications/Hammerspoon.app/Contents/Frameworks/hs/hs"],
          "target": "/opt/homebrew/bin/hs"
        }
      ]
    }
  ]
}`))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	want := []string{"/Applications/Hammerspoon.app"}
	if len(status.AppPaths) != len(want) || status.AppPaths[0] != want[0] {
		t.Fatalf("app paths got %#v, want %#v", status.AppPaths, want)
	}
}

func TestCLIBrewPackageManagerCachesTapsAndPackageStatus(t *testing.T) {
	t.Parallel()

	brewPath := writeMockBrew(t)
	manager := NewCLIBrewPackageManager(brewPath, "")

	start := make(chan struct{})
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for range 8 {
		for _, tap := range []string{"homebrew/core", "custom/tools"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := manager.TapInstalled(t.Context(), tap)
				errs <- err
			}()
		}
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("tap status: %s", err)
		}
	}
	if got := mockBrewCommandCount(t, brewPath, "tap"); got != 1 {
		t.Fatalf("brew tap calls got %d, want 1", got)
	}

	for range 2 {
		status, err := manager.PackageStatus(t.Context(), "bat", brewPackageTypeFormula)
		if err != nil {
			t.Fatalf("package status: %s", err)
		}
		if status.CandidateVersion != "0.26.1" {
			t.Fatalf("unexpected package status: %#v", status)
		}
	}
	if got := mockBrewCommandCount(t, brewPath, "info "); got != 1 {
		t.Fatalf("brew info calls got %d, want 1", got)
	}
}

func TestCLIBrewPackageManagerMutationInvalidatesStatus(t *testing.T) {
	t.Parallel()

	brewPath := writeMockBrew(t)
	manager := NewCLIBrewPackageManager(brewPath, "")

	before, err := manager.PackageStatus(t.Context(), "bat", brewPackageTypeFormula)
	if err != nil {
		t.Fatalf("status before install: %s", err)
	}
	if before.Installed {
		t.Fatal("bat should initially be absent")
	}
	if err := manager.InstallPackage(t.Context(), "bat", brewPackageTypeFormula); err != nil {
		t.Fatalf("install package: %s", err)
	}
	after, err := manager.PackageStatus(t.Context(), "bat", brewPackageTypeFormula)
	if err != nil {
		t.Fatalf("status after install: %s", err)
	}
	if !after.Installed {
		t.Fatal("cache did not observe package after mutation")
	}
	if got := mockBrewCommandCount(t, brewPath, "info "); got != 2 {
		t.Fatalf("brew info calls got %d, want 2 after invalidation", got)
	}
}

func writeMockBrew(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "brew")
	contents := `#!/bin/sh
printf '%s\n' "$*" >> "${0}.log"
case "$1" in
  tap)
    if [ "$#" -eq 1 ]; then
      printf '%s\n' 'homebrew/core' 'custom/tools'
    fi
    ;;
  info)
    if [ -f "${0}.installed" ]; then
      installed='[{"version":"0.26.0","installed_on_request":true}]'
      linked='"0.26.0"'
    else
      installed='[]'
      linked='null'
    fi
    printf '{"formulae":[{"name":"bat","full_name":"bat","versions":{"stable":"0.26.1"},"installed":%s,"linked_keg":%s,"outdated":false,"pinned":false}],"casks":[]}\n' "$installed" "$linked"
    ;;
  install)
    : > "${0}.installed"
    ;;
esac
`
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write mock brew: %s", err)
	}
	return path
}

func mockBrewCommandCount(t *testing.T, brewPath string, prefix string) int {
	t.Helper()

	content, err := os.ReadFile(brewPath + ".log")
	if err != nil {
		t.Fatalf("read mock brew log: %s", err)
	}
	count := 0
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}
