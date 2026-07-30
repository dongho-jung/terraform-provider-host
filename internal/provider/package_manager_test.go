package provider

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCLIPackageManagerSharesDNFSnapshot(t *testing.T) {
	t.Parallel()

	dnfPath := writeMockDNF(t)
	manager := NewCLIPackageManager(dnfPath, dnfPath)
	names := []string{"git", "glib2", "git.x86_64"}

	start := make(chan struct{})
	errs := make(chan error, len(names)*6)
	var wg sync.WaitGroup
	for range 6 {
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

	if got := mockDNFCommandCount(t, dnfPath, "--installed"); got != 1 {
		t.Fatalf("installed snapshot calls got %d, want 1", got)
	}
}

func TestCLIPackageManagerCachesVersionsAndInvalidatesAfterMutation(t *testing.T) {
	t.Parallel()

	dnfPath := writeMockDNF(t)
	manager := NewCLIPackageManager(dnfPath, dnfPath)

	for range 2 {
		status, err := manager.PackageStatusWithOptions(t.Context(), "git", true)
		if err != nil {
			t.Fatalf("package status: %s", err)
		}
		if status.CandidateVersion != "2.52.0-1.fc44" || status.UpgradeVersion != "2.52.0-1.fc44" {
			t.Fatalf("unexpected version status: %#v", status)
		}
	}
	if got := mockDNFCommandCount(t, dnfPath, "--installed"); got != 1 {
		t.Fatalf("installed snapshot calls got %d, want 1", got)
	}
	if got := mockDNFCommandCount(t, dnfPath, "--latest-limit=1"); got != 2 {
		t.Fatalf("candidate/upgrade calls got %d, want 2 total", got)
	}

	before, err := manager.PackageStatusWithOptions(t.Context(), "glib2", false)
	if err != nil {
		t.Fatalf("status before mutation: %s", err)
	}
	if before.ReasonUser {
		t.Fatal("glib2 should initially be dependency-installed")
	}
	if err := manager.MarkUserPackages(t.Context(), []string{"glib2"}); err != nil {
		t.Fatalf("mark user package: %s", err)
	}
	after, err := manager.PackageStatusWithOptions(t.Context(), "glib2", false)
	if err != nil {
		t.Fatalf("status after mutation: %s", err)
	}
	if !after.ReasonUser || after.InstallReason != dnfPackageInstallReasonUser {
		t.Fatalf("mutation was not observed after cache invalidation: %#v", after)
	}
	if got := mockDNFCommandCount(t, dnfPath, "--installed"); got != 2 {
		t.Fatalf("installed snapshot calls after mutation got %d, want 2", got)
	}
}

func TestParseDNFQueriesSupportsArchitectureQualifiedNames(t *testing.T) {
	t.Parallel()

	snapshot := parseDNFInstalledSnapshot([]byte("git\tx86_64\t2.51.0-1.fc44\tUser\n"))
	if !snapshot["git"].Installed || !snapshot["git.x86_64"].Installed {
		t.Fatalf("architecture aliases missing from snapshot: %#v", snapshot)
	}
	version := parseDNFVersionQuery([]byte("git\tx86_64\t2.52.0-1.fc44\n"), "git.x86_64")
	if version != "2.52.0-1.fc44" {
		t.Fatalf("version got %q", version)
	}
}

func TestCLIPackageManagerSerializesMutations(t *testing.T) {
	t.Parallel()

	dnfPath := writeMockDNF(t)
	manager := NewCLIPackageManager(dnfPath, dnfPath)
	const workers = 8
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- manager.MarkUserPackages(t.Context(), []string{"glib2"})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("DNF mutation: %s", err)
		}
	}
	if _, err := os.Stat(dnfPath + ".overlap"); !os.IsNotExist(err) {
		t.Fatalf("DNF mutations overlapped: %v", err)
	}
}

func writeMockDNF(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dnf")
	contents := `#!/bin/sh
if [ "$1" = "$0" ]; then
  shift
fi
printf '%s\n' "$*" >> "${0}.log"
if [ "$1" = "-q" ] && [ "$2" = "repoquery" ]; then
  if [ "$3" = "--installed" ]; then
    reason="Dependency"
    if [ -f "${0}.user" ]; then
      reason="User"
    fi
    printf 'git\tx86_64\t2.51.0-1.fc44\tUser\n'
    printf 'glib2\tx86_64\t2.84.4-1.fc44\t%s\n' "$reason"
  elif [ "$3" = "--upgrades" ]; then
    printf 'git\tx86_64\t2.52.0-1.fc44\n'
  else
    printf 'git\tx86_64\t2.52.0-1.fc44\n'
  fi
elif [ "$1" = "-y" ] && [ "$2" = "mark" ]; then
  if ! mkdir "${0}.active" 2>/dev/null; then
    : > "${0}.overlap"
  else
    sleep 0.01
    rmdir "${0}.active"
  fi
  : > "${0}.user"
fi
`
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write mock DNF: %s", err)
	}
	return path
}

func mockDNFCommandCount(t *testing.T, dnfPath string, fragment string) int {
	t.Helper()

	content, err := os.ReadFile(dnfPath + ".log")
	if err != nil {
		t.Fatalf("read mock DNF log: %s", err)
	}
	count := 0
	for _, line := range strings.Split(string(content), "\n") {
		if strings.Contains(line, fragment) {
			count++
		}
	}
	return count
}
