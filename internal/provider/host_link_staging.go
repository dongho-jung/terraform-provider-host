package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const hostLinkStagesDirName = "links"

// hostMaxReadableContentBytes bounds a single file's contribution to a readable
// plan attribute. Anything larger stays checksum-only so one big asset cannot
// bloat Terraform state or drown the plan output.
const hostMaxReadableContentBytes = 1 << 20

// hostReadableText returns a plain-text view of data when it is safe to surface
// in a plan diff, reporting false for binary or oversized content so callers can
// fall back to a checksum.
func hostReadableText(data []byte) (string, bool) {
	if len(data) > hostMaxReadableContentBytes {
		return "", false
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", false
	}
	return string(data), true
}

// hostLinkSourceContents collects a readable view of the source keyed by path
// relative to it, so `terraform plan` can render a line diff instead of only an
// opaque digest. Binary and oversized files are deliberately omitted; they are
// still covered by `source_digest`, which stays the authoritative drift signal.
// singleFileName keys a single-file source. It is passed in rather than derived
// from sourcePath because a staged copy is stored under its digest, and keying
// on that would make the plan compare a filename against a hash on every change.
func hostLinkSourceContents(sourcePath string, singleFileName string) (map[string]string, error) {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("read source %q: %w", sourcePath, err)
	}

	// A directory's children are keyed relative to it, while a single-file
	// source is keyed by its own name so the plan shows a filename either way.
	relative := ""
	if !info.IsDir() {
		relative = singleFileName
	}

	contents := make(map[string]string)
	if err := collectHostLinkSourceContents(contents, sourcePath, relative); err != nil {
		return nil, err
	}
	return contents, nil
}

func collectHostLinkSourceContents(contents map[string]string, path string, relative string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("read source %q: %w", path, err)
	}

	mode := info.Mode()
	switch {
	case mode.IsDir():
		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("read source directory %q: %w", path, err)
		}
		for _, entry := range entries {
			childRelative := entry.Name()
			if relative != "" {
				childRelative = filepath.Join(relative, entry.Name())
			}
			if err := collectHostLinkSourceContents(contents, filepath.Join(path, entry.Name()), childRelative); err != nil {
				return err
			}
		}
	case mode.IsRegular():
		if info.Size() > hostMaxReadableContentBytes {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read source file %q: %w", path, err)
		}
		text, ok := hostReadableText(data)
		if !ok {
			return nil
		}
		contents[relative] = text
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read source symbolic link %q: %w", path, err)
		}
		contents[relative] = "-> " + target
	default:
		return fmt.Errorf("source %q has unsupported file type %s", path, mode.Type())
	}

	return nil
}

// hostLinkStagedSourceContents reads the readable view from the version that the
// stable `current` indirection resolves to, mirroring hostLinkStagedSourceDigest.
func hostLinkStagedSourceContents(currentPath string, singleFileName string) (map[string]string, error) {
	versionPath, err := filepath.EvalSymlinks(currentPath)
	if err != nil {
		return nil, fmt.Errorf("resolve staged source %q: %w", currentPath, err)
	}

	return hostLinkSourceContents(versionPath, singleFileName)
}

func hostLinkSourceDigest(sourcePath string) (string, error) {
	digest := sha256.New()
	if err := hashHostLinkSource(digest, sourcePath, "."); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func hashHostLinkSource(digest hash.Hash, path string, relative string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("read source %q: %w", path, err)
	}

	mode := info.Mode()
	switch {
	case mode.IsDir():
		writeHostLinkDigestField(digest, "directory", relative, mode.Perm().String())
		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("read source directory %q: %w", path, err)
		}
		for _, entry := range entries {
			childRelative := filepath.Join(relative, entry.Name())
			if err := hashHostLinkSource(digest, filepath.Join(path, entry.Name()), childRelative); err != nil {
				return err
			}
		}
	case mode.IsRegular():
		writeHostLinkDigestField(digest, "file", relative, mode.Perm().String(), strconv.FormatInt(info.Size(), 10))
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open source file %q: %w", path, err)
		}
		if _, err := io.Copy(digest, file); err != nil {
			_ = file.Close()
			return fmt.Errorf("hash source file %q: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close source file %q: %w", path, err)
		}
		_, _ = digest.Write([]byte{0})
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read source symbolic link %q: %w", path, err)
		}
		writeHostLinkDigestField(digest, "symlink", relative, target)
	default:
		return fmt.Errorf("source %q has unsupported file type %s", path, mode.Type())
	}

	return nil
}

func writeHostLinkDigestField(digest hash.Hash, fields ...string) {
	for _, field := range fields {
		_, _ = digest.Write([]byte(field))
		_, _ = digest.Write([]byte{0})
	}
}

func hostLinkStageRoot(runtimeDir string, destinationPath string) (string, error) {
	linksRoot, err := providerRuntimeSubdir(runtimeDir, hostLinkStagesDirName)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(destinationPath) {
		return "", fmt.Errorf("link destination must be absolute, got %q", destinationPath)
	}

	destinationDigest := sha256.Sum256([]byte(filepath.Clean(destinationPath)))
	return filepath.Join(linksRoot, hex.EncodeToString(destinationDigest[:])), nil
}

func hostLinkStagePath(stageRoot string, sourceDigest string) string {
	return filepath.Join(stageRoot, "versions", sourceDigest)
}

// hostLinkStageCurrentPath is the stable path every staged link points at. The
// versioned copies keep content-addressed names, but routing the host link
// through a fixed indirection keeps `source_path` out of the plan diff when only
// the source content changed.
func hostLinkStageCurrentPath(stageRoot string) string {
	return filepath.Join(stageRoot, "current")
}

// hostLinkStagedSourceDigest digests the version that the stable `current`
// indirection resolves to, rather than the symbolic link itself.
func hostLinkStagedSourceDigest(currentPath string) (string, error) {
	versionPath, err := filepath.EvalSymlinks(currentPath)
	if err != nil {
		return "", fmt.Errorf("resolve staged source %q: %w", currentPath, err)
	}

	return hostLinkSourceDigest(versionPath)
}

func stageHostLinkSource(sourcePath string, destinationPath string, expectedDigest string) error {
	if !isSHA256Hex(expectedDigest) {
		return fmt.Errorf("staged source digest must be a lowercase SHA256, got %q", expectedDigest)
	}
	if filepath.Base(destinationPath) != expectedDigest {
		return fmt.Errorf("staged destination %q does not match source digest %q", destinationPath, expectedDigest)
	}

	if existingDigest, err := hostLinkSourceDigest(destinationPath); err == nil {
		if existingDigest == expectedDigest {
			return nil
		}
		if err := os.RemoveAll(destinationPath); err != nil {
			return fmt.Errorf("remove corrupt staged source %q: %w", destinationPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect staged source %q: %w", destinationPath, err)
	}

	versionsDir := filepath.Dir(destinationPath)
	if err := os.MkdirAll(versionsDir, 0o700); err != nil {
		return fmt.Errorf("create staged source directory %q: %w", versionsDir, err)
	}
	if err := os.Chmod(versionsDir, 0o700); err != nil {
		return fmt.Errorf("protect staged source directory %q: %w", versionsDir, err)
	}

	tempDir, err := os.MkdirTemp(versionsDir, ".terraform-provider-host-stage-*")
	if err != nil {
		return fmt.Errorf("create staged source temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	tempSource := filepath.Join(tempDir, "source")
	if err := copyHostLinkSource(sourcePath, tempSource); err != nil {
		return err
	}
	copiedDigest, err := hostLinkSourceDigest(tempSource)
	if err != nil {
		return fmt.Errorf("verify staged source: %w", err)
	}
	if copiedDigest != expectedDigest {
		return fmt.Errorf("staged source digest %s does not match planned digest %s", copiedDigest, expectedDigest)
	}

	if err := os.Rename(tempSource, destinationPath); err != nil {
		if existingDigest, digestErr := hostLinkSourceDigest(destinationPath); digestErr == nil && existingDigest == expectedDigest {
			return nil
		}
		return fmt.Errorf("publish staged source %q: %w", destinationPath, err)
	}
	return nil
}

func copyHostLinkSource(sourcePath string, destinationPath string) error {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return fmt.Errorf("read source %q: %w", sourcePath, err)
	}

	mode := info.Mode()
	switch {
	case mode.IsDir():
		if err := os.Mkdir(destinationPath, mode.Perm()); err != nil {
			return fmt.Errorf("create staged directory %q: %w", destinationPath, err)
		}
		entries, err := os.ReadDir(sourcePath)
		if err != nil {
			return fmt.Errorf("read source directory %q: %w", sourcePath, err)
		}
		for _, entry := range entries {
			if err := copyHostLinkSource(
				filepath.Join(sourcePath, entry.Name()),
				filepath.Join(destinationPath, entry.Name()),
			); err != nil {
				return err
			}
		}
		if err := os.Chmod(destinationPath, mode.Perm()); err != nil {
			return fmt.Errorf("set staged directory mode %q: %w", destinationPath, err)
		}
	case mode.IsRegular():
		source, err := os.Open(sourcePath)
		if err != nil {
			return fmt.Errorf("open source file %q: %w", sourcePath, err)
		}
		destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
		if err != nil {
			_ = source.Close()
			return fmt.Errorf("create staged file %q: %w", destinationPath, err)
		}
		if _, err := io.Copy(destination, source); err != nil {
			_ = destination.Close()
			_ = source.Close()
			return fmt.Errorf("copy staged file %q: %w", destinationPath, err)
		}
		if err := destination.Sync(); err != nil {
			_ = destination.Close()
			_ = source.Close()
			return fmt.Errorf("sync staged file %q: %w", destinationPath, err)
		}
		if err := destination.Close(); err != nil {
			_ = source.Close()
			return fmt.Errorf("close staged file %q: %w", destinationPath, err)
		}
		if err := source.Close(); err != nil {
			return fmt.Errorf("close source file %q: %w", sourcePath, err)
		}
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(sourcePath)
		if err != nil {
			return fmt.Errorf("read source symbolic link %q: %w", sourcePath, err)
		}
		if err := os.Symlink(target, destinationPath); err != nil {
			return fmt.Errorf("copy staged symbolic link %q: %w", destinationPath, err)
		}
	default:
		return fmt.Errorf("source %q has unsupported file type %s", sourcePath, mode.Type())
	}

	return nil
}

func cleanupHostLinkStages(stageRoot string, keepDigest string) error {
	if err := validateHostLinkStageRoot(stageRoot); err != nil {
		return err
	}
	if !isSHA256Hex(keepDigest) {
		return fmt.Errorf("staged source digest must be a lowercase SHA256, got %q", keepDigest)
	}

	versionsDir := filepath.Join(stageRoot, "versions")
	entries, err := os.ReadDir(versionsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read staged source versions %q: %w", versionsDir, err)
	}
	for _, entry := range entries {
		if entry.Name() == keepDigest {
			continue
		}
		if err := os.RemoveAll(filepath.Join(versionsDir, entry.Name())); err != nil {
			return fmt.Errorf("remove stale staged source %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func removeHostLinkStageRoot(stageRoot string) error {
	if stageRoot == "" {
		return nil
	}
	if err := validateHostLinkStageRoot(stageRoot); err != nil {
		return err
	}
	if err := os.RemoveAll(stageRoot); err != nil {
		return fmt.Errorf("remove staged source directory %q: %w", stageRoot, err)
	}
	return nil
}

func validateHostLinkStageRoot(stageRoot string) error {
	clean := filepath.Clean(stageRoot)
	if !filepath.IsAbs(clean) {
		return fmt.Errorf("staged source root must be absolute, got %q", stageRoot)
	}
	if filepath.Base(filepath.Dir(clean)) != hostLinkStagesDirName || !isSHA256Hex(filepath.Base(clean)) {
		return fmt.Errorf("refusing unsafe staged source root %q", stageRoot)
	}
	return nil
}

func isSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
