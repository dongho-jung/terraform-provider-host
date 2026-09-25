package provider

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const macOSDockDomain = "com.apple.dock"

const macOSDockReadCacheTTL = 500 * time.Millisecond

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

	cacheMu    sync.RWMutex
	cachedDock *MacOSDockSpec
	cacheUntil time.Time
	readGroup  singleflight.Group
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

	if cached, ok := m.readCachedDock(); ok {
		return cached, nil
	}

	value, err, _ := m.readGroup.Do("dock", func() (any, error) {
		if cached, ok := m.readCachedDock(); ok {
			return cached, nil
		}
		dock, err := m.readDockUncached(ctx)
		if err != nil {
			return MacOSDockSpec{}, err
		}
		m.cacheDock(dock)
		return dock, nil
	})
	if err != nil {
		return MacOSDockSpec{}, err
	}
	dock, ok := value.(MacOSDockSpec)
	if !ok {
		return MacOSDockSpec{}, fmt.Errorf("unexpected macOS Dock read result %T", value)
	}
	return cloneMacOSDockSpec(dock), nil
}

func (m *CLIMacOSDockManager) readDockUncached(ctx context.Context) (MacOSDockSpec, error) {
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
	m.cacheDock(spec)
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

func (m *CLIMacOSDockManager) readCachedDock() (MacOSDockSpec, bool) {
	m.cacheMu.RLock()
	defer m.cacheMu.RUnlock()
	if m.cachedDock == nil || !time.Now().Before(m.cacheUntil) {
		return MacOSDockSpec{}, false
	}
	return cloneMacOSDockSpec(*m.cachedDock), true
}

func (m *CLIMacOSDockManager) cacheDock(spec MacOSDockSpec) {
	cloned := cloneMacOSDockSpec(spec)
	m.cacheMu.Lock()
	m.cachedDock = &cloned
	m.cacheUntil = time.Now().Add(macOSDockReadCacheTTL)
	m.cacheMu.Unlock()
}

func cloneMacOSDockSpec(spec MacOSDockSpec) MacOSDockSpec {
	return MacOSDockSpec{
		Apps:    append([]string(nil), spec.Apps...),
		Folders: append([]string(nil), spec.Folders...),
	}
}

func resolveMacOSDockPathForHome(label string, item string, wantApp bool, homeDir string) (string, error) {
	path := strings.TrimSpace(item)
	if path == "" {
		return "", fmt.Errorf("%s entries must be non-empty paths", label)
	}
	if strings.Contains(path, "\x00") {
		return "", fmt.Errorf("%q must not contain NUL bytes", path)
	}

	resolved, err := expandHostPathWithHome(path, homeDir)
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

// macOSDockEntry renders one tile as an XML property list fragment.
//
// `defaults` accepts a value in the old-style plist syntax too, but that syntax
// has no number type: a bare 15 arrives as the string "15". The Dock reads
// _CFURLStringType, and the stack size keys, as numbers, and a tile whose type
// is text is one the Dock cannot resolve, so it draws a question mark instead
// of the icon and drops the tile the next time it writes the domain. XML keeps
// every value's type.
func macOSDockEntry(path string, tileType string) string {
	label := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	urlString := macOSDockFileURL(path)

	var tile strings.Builder
	tile.WriteString(`<dict><key>tile-data</key><dict><key>file-data</key><dict><key>_CFURLString</key>`)
	tile.WriteString(macOSDockXMLString(urlString))
	tile.WriteString(`<key>_CFURLStringType</key><integer>15</integer></dict><key>file-label</key>`)
	tile.WriteString(macOSDockXMLString(label))
	if tileType == "directory-tile" {
		tile.WriteString(`<key>arrangement</key><integer>2</integer>`)
		tile.WriteString(`<key>displayas</key><integer>0</integer>`)
		tile.WriteString(`<key>preferreditemsize</key><integer>-1</integer>`)
		tile.WriteString(`<key>showas</key><integer>1</integer>`)
	}
	tile.WriteString(`</dict><key>tile-type</key>`)
	tile.WriteString(macOSDockXMLString(tileType))
	tile.WriteString(`</dict>`)

	return tile.String()
}

func macOSDockXMLString(value string) string {
	var escaped strings.Builder
	xml.EscapeText(&escaped, []byte(value))
	return "<string>" + escaped.String() + "</string>"
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
