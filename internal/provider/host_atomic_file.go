package provider

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeHostFileAtomically replaces path with a fully written file from the
// same directory. Keeping the temporary file beside the destination makes the
// final rename atomic on macOS and Linux and prevents interrupted writes from
// leaving a truncated destination.
func writeHostFileAtomically(path string, content []byte, mode os.FileMode) (returnErr error) {
	var destinationInfo os.FileInfo
	if info, err := os.Stat(path); err == nil {
		destinationInfo = info
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat destination %q: %w", path, err)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(path), ".terraform-provider-host-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", path, err)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) && returnErr == nil {
			returnErr = fmt.Errorf("remove temporary file %q: %w", tempPath, err)
		}
	}()

	if err := tempFile.Chmod(mode.Perm()); err != nil {
		return fmt.Errorf("chmod temporary file for %q: %w", path, err)
	}
	if destinationInfo != nil {
		if err := preserveAtomicFileOwnership(tempFile, destinationInfo); err != nil {
			return fmt.Errorf("preserve ownership for %q: %w", path, err)
		}
	}
	if _, err := tempFile.Write(content); err != nil {
		return fmt.Errorf("write temporary file for %q: %w", path, err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("sync temporary file for %q: %w", path, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temporary file for %q: %w", path, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("atomically replace %q: %w", path, err)
	}

	return nil
}
