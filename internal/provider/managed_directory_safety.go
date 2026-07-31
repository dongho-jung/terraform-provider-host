package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validateDirectoryRemoval(path string, homeDir string, runtimeDir string, recursive bool) error {
	resolvedPath, err := canonicalRemovalPath(path)
	if err != nil {
		return err
	}
	if filepath.Dir(resolvedPath) == resolvedPath {
		return fmt.Errorf("refusing to remove filesystem root %q", resolvedPath)
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve Terraform working directory before directory removal: %w", err)
	}
	protected := []struct {
		name string
		path string
	}{
		{name: "target user home directory", path: homeDir},
		{name: "provider runtime directory", path: runtimeDir},
		{name: "Terraform working directory", path: workingDir},
	}
	for _, candidate := range protected {
		if strings.TrimSpace(candidate.path) == "" {
			continue
		}
		protectedPath, err := canonicalRemovalPath(candidate.path)
		if err != nil {
			return fmt.Errorf("resolve protected %s: %w", candidate.name, err)
		}
		if resolvedPath == protectedPath || (recursive && directoryContainsPath(resolvedPath, protectedPath)) {
			operation := "remove"
			if recursive {
				operation = "recursively remove"
			}
			return fmt.Errorf(
				"refusing to %s %q because it contains the %s %q",
				operation,
				resolvedPath,
				candidate.name,
				protectedPath,
			)
		}
	}

	return nil
}

func canonicalRemovalPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("directory removal path must be non-empty")
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("directory removal path must be absolute, got %q", path)
	}

	current := clean
	var missingComponents []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(missingComponents) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missingComponents[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolve directory removal path %q: %w", clean, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolve directory removal path %q: %w", clean, err)
		}
		missingComponents = append(missingComponents, filepath.Base(current))
		current = parent
	}
}

func directoryContainsPath(directory string, path string) bool {
	relative, err := filepath.Rel(directory, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}
