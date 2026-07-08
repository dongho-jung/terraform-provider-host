package provider

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsAURNoResultError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"yay not found", fmt.Errorf("yay -Si missing failed: exit status 1\nerror: missing not found"), true},
		{"paru no results", fmt.Errorf("paru -Si missing failed: exit status 1\nerror: no results found"), true},
		{"pacman target not found", fmt.Errorf("pacman -Si missing failed: exit status 1\nerror: package 'missing' was not found"), true},
		{"unrelated failure", fmt.Errorf("yay -Si foo failed: exit status 1 conflicting dependencies"), false},
		{"other exit status", fmt.Errorf("yay -Si foo failed: exit status 8 error: foo not found"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := isAURNoResultError(tc.err); got != tc.want {
				t.Fatalf("isAURNoResultError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestCLIAURPackageManagerCandidateIsNewer(t *testing.T) {
	t.Parallel()

	vercmpPath, err := exec.LookPath("vercmp")
	if err != nil {
		t.Skip("vercmp not available")
	}

	manager := &CLIAURPackageManager{vercmpPath: vercmpPath}

	newer, err := manager.candidateIsNewer(t.Context(), "2.0.0-1", "1.0.0-1")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !newer {
		t.Fatal("expected 2.0.0-1 to be newer than 1.0.0-1")
	}

	newer, err = manager.candidateIsNewer(t.Context(), "1.0.0-1", "1.0.0-2")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if newer {
		t.Fatal("expected 1.0.0-1 to not be newer than 1.0.0-2")
	}
}

func TestCLIAURPackageManagerPackageStatusLocalOnly(t *testing.T) {
	t.Parallel()

	pacmanPath, err := exec.LookPath("pacman")
	if err != nil {
		t.Skip("pacman not available")
	}
	helperPath, err := exec.LookPath("yay")
	if err != nil {
		helperPath, err = exec.LookPath("paru")
		if err != nil {
			t.Skip("no AUR helper available")
		}
	}

	manager := NewCLIAURPackageManager(filepath.Base(helperPath), helperPath, "", NewCLIPacmanPackageManager(pacmanPath, ""))

	// pacman itself is always installed on an Arch host; includeRemote=false
	// must resolve the status without ever invoking the AUR helper.
	status, err := manager.PackageStatus(t.Context(), "pacman", false)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !status.Installed {
		t.Fatal("expected pacman to be installed")
	}
	if status.InstalledVersion == "" {
		t.Fatal("expected installed version")
	}
	if status.CandidateVersion != "" {
		t.Fatalf("expected no candidate version without remote lookup, got %q", status.CandidateVersion)
	}
	if status.UpgradeVersion != "" {
		t.Fatalf("expected no upgrade version without remote lookup, got %q", status.UpgradeVersion)
	}
}
