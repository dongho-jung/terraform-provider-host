package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRecursiveDirectoryRemovalRefusesProtectedTrees(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	runtimeDir := filepath.Join(homeDir, ".local", "state", providerRuntimeDirName)
	for _, path := range []string{homeDir, runtimeDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("mkdir %q: %s", path, err)
		}
	}
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %s", err)
	}
	filesystemRoot := filepath.VolumeName(workingDir) + string(os.PathSeparator)

	for name, path := range map[string]string{
		"filesystem root":   filesystemRoot,
		"home":              homeDir,
		"home parent":       filepath.Dir(homeDir),
		"runtime":           runtimeDir,
		"runtime parent":    filepath.Dir(runtimeDir),
		"working directory": workingDir,
		"working parent":    filepath.Dir(workingDir),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateDirectoryRemoval(path, homeDir, runtimeDir, true); err == nil {
				t.Fatalf("expected recursive removal of %q to be refused", path)
			}
		})
	}
}

func TestValidateRecursiveDirectoryRemovalAllowsContainedDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	runtimeDir := filepath.Join(homeDir, ".local", "state", providerRuntimeDirName)
	managedDir := filepath.Join(homeDir, "projects", "retired")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatalf("mkdir runtime: %s", err)
	}
	if err := os.MkdirAll(managedDir, 0o700); err != nil {
		t.Fatalf("mkdir managed directory: %s", err)
	}

	if err := validateDirectoryRemoval(managedDir, homeDir, runtimeDir, true); err != nil {
		t.Fatalf("expected contained managed directory to be removable: %s", err)
	}
}

func TestValidateRecursiveDirectoryRemovalResolvesSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	runtimeDir := filepath.Join(homeDir, ".local", "state", providerRuntimeDirName)
	alias := filepath.Join(root, "home-alias")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatalf("mkdir runtime: %s", err)
	}
	if err := os.Symlink(homeDir, alias); err != nil {
		t.Skipf("symlink unavailable: %s", err)
	}

	if err := validateDirectoryRemoval(alias, homeDir, runtimeDir, true); err == nil {
		t.Fatal("expected symlink to protected home directory to be refused")
	}
}

func TestValidateDirectoryRemovalAllowsProtectedParentWhenNonRecursive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	runtimeDir := filepath.Join(homeDir, ".local", "state", providerRuntimeDirName)
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatalf("mkdir runtime: %s", err)
	}

	if err := validateDirectoryRemoval(root, homeDir, runtimeDir, false); err != nil {
		t.Fatalf("non-recursive parent removal is inherently bounded and should be allowed: %s", err)
	}
	if err := validateDirectoryRemoval(homeDir, homeDir, runtimeDir, false); err == nil {
		t.Fatal("expected exact protected home directory to be refused")
	}
}
