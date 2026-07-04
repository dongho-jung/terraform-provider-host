package provider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const macOSDockDomain = "com.apple.dock"

type MacOSDockSpec struct {
	Apps    []string
	Folders []string
}

type MacOSDockManager interface {
	ReadDock(ctx context.Context) (MacOSDockSpec, error)
	WriteDock(ctx context.Context, spec MacOSDockSpec) error
	RestartDock(ctx context.Context) error
}

type CLIMacOSDockManager struct {
	defaultsPath string
	killallPath  string
	run          macOSCommandRunner
}

func NewCLIMacOSDockManager(defaultsPath string, killallPath string) MacOSDockManager {
	return &CLIMacOSDockManager{
		defaultsPath: defaultsPath,
		killallPath:  killallPath,
		run:          runMacOSCommand,
	}
}

func (m *CLIMacOSDockManager) ReadDock(ctx context.Context) (MacOSDockSpec, error) {
	if m.defaultsPath == "" {
		return MacOSDockSpec{}, fmt.Errorf("defaults command not found")
	}

	appsOut, err := m.readDockArray(ctx, "persistent-apps")
	if err != nil {
		return MacOSDockSpec{}, err
	}
	foldersOut, err := m.readDockArray(ctx, "persistent-others")
	if err != nil {
		return MacOSDockSpec{}, err
	}

	return MacOSDockSpec{
		Apps:    parseMacOSDockFileURLs(string(appsOut)),
		Folders: parseMacOSDockFileURLs(string(foldersOut)),
	}, nil
}

func (m *CLIMacOSDockManager) WriteDock(ctx context.Context, spec MacOSDockSpec) error {
	if m.defaultsPath == "" {
		return fmt.Errorf("defaults command not found")
	}

	if err := m.writeDockArray(ctx, "persistent-apps", macOSDockEntries(spec.Apps, "file-tile")); err != nil {
		return err
	}
	if err := m.writeDockArray(ctx, "persistent-others", macOSDockEntries(spec.Folders, "directory-tile")); err != nil {
		return err
	}
	return nil
}

func (m *CLIMacOSDockManager) RestartDock(ctx context.Context) error {
	if m.killallPath == "" {
		return fmt.Errorf("killall command not found")
	}
	_, _ = m.run(ctx, m.killallPath, "Dock")
	return nil
}

func (m *CLIMacOSDockManager) readDockArray(ctx context.Context, key string) ([]byte, error) {
	out, err := m.run(ctx, m.defaultsPath, "read", macOSDockDomain, key)
	if err != nil && isMacOSDefaultsMissingError(err) {
		return []byte("()"), nil
	}
	return out, err
}

func (m *CLIMacOSDockManager) writeDockArray(ctx context.Context, key string, entries []string) error {
	args := []string{"write", macOSDockDomain, key, "-array"}
	args = append(args, entries...)
	_, err := m.run(ctx, m.defaultsPath, args...)
	return err
}

func resolveMacOSDockPathForHome(label string, item string, wantApp bool, homeDir string) (string, error) {
	path := strings.TrimSpace(item)
	if path == "" {
		return "", fmt.Errorf("%s entries must be non-empty paths", label)
	}
	if strings.Contains(path, "\x00") {
		return "", fmt.Errorf("%q must not contain NUL bytes", path)
	}

	resolved, err := expandHostPathForHome(path, homeDir)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", fmt.Errorf("path %q is not readable: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q must be a directory", resolved)
	}
	if wantApp && filepath.Ext(resolved) != ".app" {
		return "", fmt.Errorf("app path %q must end with .app", resolved)
	}
	return resolved, nil
}

func macOSDockEntries(paths []string, tileType string) []string {
	entries := make([]string, 0, len(paths))
	for _, path := range paths {
		entries = append(entries, macOSDockEntry(path, tileType))
	}
	return entries
}

func macOSDockEntry(path string, tileType string) string {
	label := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	urlString := macOSDockFileURL(path)

	tileData := `"file-data"={"_CFURLString"=` + strconv.Quote(urlString) + `; "_CFURLStringType"=15;}; "file-label"=` + strconv.Quote(label) + `;`
	if tileType == "directory-tile" {
		tileData += ` arrangement=2; displayas=0; preferreditemsize="-1"; showas=1;`
	}
	return `{"tile-data"={` + tileData + `}; "tile-type"=` + strconv.Quote(tileType) + `;}`
}

func macOSDockFileURL(path string) string {
	cleaned := filepath.Clean(path)
	if !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return (&url.URL{Scheme: "file", Path: cleaned}).String()
}

func parseMacOSDockFileURLs(output string) []string {
	pattern := regexp.MustCompile(`"_CFURLString"\s*=\s*"([^"]+)"`)
	matches := pattern.FindAllStringSubmatch(output, -1)
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		parsed, err := url.Parse(match[1])
		if err != nil || parsed.Scheme != "file" {
			continue
		}
		path := strings.TrimSuffix(parsed.Path, "/")
		if path == "" {
			path = "/"
		}
		paths = append(paths, path)
	}
	return paths
}
