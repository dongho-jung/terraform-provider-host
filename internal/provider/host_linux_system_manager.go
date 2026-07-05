package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	hostSysctlDir     = "/etc/sysctl.d"
	hostSystemdDir    = "/etc/systemd/system"
	hostFstabPath     = "/etc/fstab"
	hostFstabMarkerID = "terraform-provider-host"
)

type SysctlManager interface {
	Sysctl(ctx context.Context, key string) (string, error)
	SyncSysctl(ctx context.Context, spec SysctlSpec) error
	DeleteSysctl(ctx context.Context, key string) error
	NeedsPrivilegeEscalation() bool
}

type SysctlSpec struct {
	Key   string
	Value string
}

type CLISysctlManager struct {
	sysctlPath string
	sudoPath   string
}

func NewCLISysctlManager(sysctlPath string, sudoPath string) *CLISysctlManager {
	return &CLISysctlManager{
		sysctlPath: sysctlPath,
		sudoPath:   sudoPath,
	}
}

func (m *CLISysctlManager) Sysctl(ctx context.Context, key string) (string, error) {
	if err := validateHostSysctlKey(key); err != nil {
		return "", err
	}

	out, err := runHostSystemCommand(ctx, false, m.sudoPath, "sysctl", m.sysctlPath, "-n", key)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *CLISysctlManager) SyncSysctl(ctx context.Context, spec SysctlSpec) error {
	if err := validateHostSysctlSpec(spec); err != nil {
		return err
	}

	if err := writeProtectedFile(ctx, m.sudoPath, sysctlManagedPath(spec.Key), []byte(formatSysctlContent(spec)), 0o644); err != nil {
		return err
	}
	_, err := runHostSystemCommand(ctx, true, m.sudoPath, "sysctl", m.sysctlPath, "-w", spec.Key+"="+spec.Value)
	return err
}

func (m *CLISysctlManager) DeleteSysctl(ctx context.Context, key string) error {
	if err := validateHostSysctlKey(key); err != nil {
		return err
	}

	return removeProtectedFile(ctx, m.sudoPath, sysctlManagedPath(key))
}

func (m *CLISysctlManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

type SystemdUnitManager interface {
	Unit(ctx context.Context, name string) (SystemdUnitState, bool, error)
	SyncUnit(ctx context.Context, spec SystemdUnitSpec) error
	DeleteUnit(ctx context.Context, name string) error
	NeedsPrivilegeEscalation() bool
}

type SystemdUnitSpec struct {
	Name    string
	Content string
	Mode    os.FileMode
}

type SystemdUnitState struct {
	Name    string
	Path    string
	Content string
	Mode    os.FileMode
}

type CLISystemdUnitManager struct {
	systemctlPath string
	sudoPath      string
}

func NewCLISystemdUnitManager(systemctlPath string, sudoPath string) *CLISystemdUnitManager {
	return &CLISystemdUnitManager{
		systemctlPath: systemctlPath,
		sudoPath:      sudoPath,
	}
}

func (m *CLISystemdUnitManager) Unit(ctx context.Context, name string) (SystemdUnitState, bool, error) {
	if err := validateSystemdUnitName(name); err != nil {
		return SystemdUnitState{}, false, err
	}

	path := systemdUnitPath(name)
	content, exists, err := readProtectedFile(path)
	if err != nil || !exists {
		return SystemdUnitState{}, exists, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return SystemdUnitState{}, false, err
	}

	return SystemdUnitState{
		Name:    name,
		Path:    path,
		Content: string(content),
		Mode:    info.Mode().Perm(),
	}, true, nil
}

func (m *CLISystemdUnitManager) SyncUnit(ctx context.Context, spec SystemdUnitSpec) error {
	if err := validateSystemdUnitSpec(spec); err != nil {
		return err
	}

	if err := writeProtectedFile(ctx, m.sudoPath, systemdUnitPath(spec.Name), []byte(spec.Content), spec.Mode); err != nil {
		return err
	}
	return m.daemonReload(ctx)
}

func (m *CLISystemdUnitManager) DeleteUnit(ctx context.Context, name string) error {
	if err := validateSystemdUnitName(name); err != nil {
		return err
	}

	if err := removeProtectedFile(ctx, m.sudoPath, systemdUnitPath(name)); err != nil {
		return err
	}
	return m.daemonReload(ctx)
}

func (m *CLISystemdUnitManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

func (m *CLISystemdUnitManager) daemonReload(ctx context.Context) error {
	_, err := runHostSystemCommand(ctx, true, m.sudoPath, "systemd", m.systemctlPath, "daemon-reload")
	return err
}

type FstabManager interface {
	Entry(ctx context.Context, name string) (FstabEntry, bool, error)
	SyncEntry(ctx context.Context, entry FstabEntry) error
	DeleteEntry(ctx context.Context, name string) error
	NeedsPrivilegeEscalation() bool
}

type FstabEntry struct {
	Name       string
	Device     string
	MountPoint string
	FSType     string
	Options    string
	Dump       int64
	Pass       int64
}

type HostFstabManager struct {
	path     string
	sudoPath string
}

func NewHostFstabManager(sudoPath string) *HostFstabManager {
	return &HostFstabManager{
		path:     hostFstabPath,
		sudoPath: sudoPath,
	}
}

func (m *HostFstabManager) Entry(ctx context.Context, name string) (FstabEntry, bool, error) {
	if err := validateHostFstabName(name); err != nil {
		return FstabEntry{}, false, err
	}

	content, exists, err := readProtectedFile(m.path)
	if err != nil || !exists {
		return FstabEntry{}, false, err
	}
	return readFstabManagedEntry(string(content), name)
}

func (m *HostFstabManager) SyncEntry(ctx context.Context, entry FstabEntry) error {
	if err := validateHostFstabEntry(entry); err != nil {
		return err
	}

	content, exists, err := readProtectedFile(m.path)
	if err != nil {
		return err
	}
	current := ""
	if exists {
		current = string(content)
	}

	next, err := upsertFstabManagedEntry(current, entry)
	if err != nil {
		return err
	}
	return writeProtectedFile(ctx, m.sudoPath, m.path, []byte(next), 0o644)
}

func (m *HostFstabManager) DeleteEntry(ctx context.Context, name string) error {
	if err := validateHostFstabName(name); err != nil {
		return err
	}

	content, exists, err := readProtectedFile(m.path)
	if err != nil || !exists {
		return err
	}
	next, changed, err := removeFstabManagedEntry(string(content), name)
	if err != nil || !changed {
		return err
	}
	return writeProtectedFile(ctx, m.sudoPath, m.path, []byte(next), 0o644)
}

func (m *HostFstabManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

func sysctlManagedPath(key string) string {
	return filepath.Join(hostSysctlDir, "99-terraform-host-"+sanitizeSysctlKey(key)+".conf")
}

func sanitizeSysctlKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func systemdUnitPath(name string) string {
	return filepath.Join(hostSystemdDir, name)
}

func formatSysctlContent(spec SysctlSpec) string {
	return fmt.Sprintf("%s = %s\n", spec.Key, spec.Value)
}

func validateHostSysctlSpec(spec SysctlSpec) error {
	if err := validateHostSysctlKey(spec.Key); err != nil {
		return err
	}
	return validateHostSysctlValue(spec.Value)
}

func validateHostSysctlKey(key string) error {
	if strings.TrimSpace(key) != key || key == "" {
		return fmt.Errorf("sysctl key must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.HasPrefix(key, "-") {
		return fmt.Errorf("sysctl key must not start with '-'")
	}
	for _, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("sysctl key contains invalid character %q", r)
	}
	return nil
}

func validateHostSysctlValue(value string) error {
	if value == "" {
		return fmt.Errorf("sysctl value must be non-empty")
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("sysctl value must not contain newlines or NUL bytes")
	}
	return nil
}

func validateSystemdUnitSpec(spec SystemdUnitSpec) error {
	if err := validateSystemdUnitName(spec.Name); err != nil {
		return err
	}
	if spec.Content == "" {
		return fmt.Errorf("unit content must be non-empty")
	}
	if spec.Mode.Perm() == 0 || spec.Mode.Perm() > 0o777 {
		return fmt.Errorf("unit mode must be between 0001 and 0777")
	}
	return nil
}

func validateHostFstabEntry(entry FstabEntry) error {
	if err := validateHostFstabName(entry.Name); err != nil {
		return err
	}
	for label, value := range map[string]string{
		"device":      entry.Device,
		"mount_point": entry.MountPoint,
		"fs_type":     entry.FSType,
		"options":     entry.Options,
	} {
		if err := validateHostFstabField(label, value); err != nil {
			return err
		}
	}
	if entry.MountPoint != "none" && !strings.HasPrefix(entry.MountPoint, "/") {
		return fmt.Errorf("mount_point must be an absolute path or none")
	}
	if entry.Dump < 0 {
		return fmt.Errorf("dump must be zero or greater")
	}
	if entry.Pass < 0 {
		return fmt.Errorf("pass must be zero or greater")
	}
	return nil
}

func validateHostFstabName(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("fstab entry name must be non-empty and must not contain leading or trailing whitespace")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("fstab entry name contains invalid character %q", r)
	}
	return nil
}

func validateHostFstabField(label string, value string) error {
	if strings.TrimSpace(value) != value || value == "" {
		return fmt.Errorf("%s must be non-empty and must not contain leading or trailing whitespace", label)
	}
	if strings.ContainsAny(value, " \t\r\n\x00") {
		return fmt.Errorf("%s must not contain whitespace or NUL bytes", label)
	}
	return nil
}

func readFstabManagedEntry(content string, name string) (FstabEntry, bool, error) {
	block, exists, err := fstabManagedBlock(content, name)
	if err != nil || !exists {
		return FstabEntry{}, exists, err
	}
	return parseFstabManagedEntry(name, block)
}

func parseFstabManagedEntry(name string, block string) (FstabEntry, bool, error) {
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 6 {
			return FstabEntry{}, true, fmt.Errorf("managed fstab entry %q must contain exactly 6 fields", name)
		}
		dump, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil {
			return FstabEntry{}, true, fmt.Errorf("invalid fstab dump value for %q: %w", name, err)
		}
		pass, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil {
			return FstabEntry{}, true, fmt.Errorf("invalid fstab pass value for %q: %w", name, err)
		}
		entry := FstabEntry{
			Name:       name,
			Device:     fields[0],
			MountPoint: fields[1],
			FSType:     fields[2],
			Options:    fields[3],
			Dump:       dump,
			Pass:       pass,
		}
		if err := validateHostFstabEntry(entry); err != nil {
			return FstabEntry{}, true, err
		}
		return entry, true, nil
	}
	return FstabEntry{}, true, fmt.Errorf("managed fstab entry %q is empty", name)
}

func upsertFstabManagedEntry(content string, entry FstabEntry) (string, error) {
	block := formatFstabManagedBlock(entry)
	next, changed, err := replaceFstabManagedBlock(content, entry.Name, block)
	if err != nil {
		return "", err
	}
	if changed {
		return next, nil
	}
	if next != "" && !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	return next + block, nil
}

func removeFstabManagedEntry(content string, name string) (string, bool, error) {
	return replaceFstabManagedBlock(content, name, "")
}

func replaceFstabManagedBlock(content string, name string, replacement string) (string, bool, error) {
	start := fstabStartMarker(name)
	end := fstabEndMarker(name)
	startIndex := strings.Index(content, start)
	if startIndex == -1 {
		return content, false, nil
	}
	endIndex := strings.Index(content[startIndex:], end)
	if endIndex == -1 {
		return "", false, fmt.Errorf("managed fstab entry %q is missing end marker", name)
	}
	endIndex = startIndex + endIndex + len(end)
	if endIndex < len(content) && content[endIndex] == '\n' {
		endIndex++
	}
	return content[:startIndex] + replacement + content[endIndex:], true, nil
}

func fstabManagedBlock(content string, name string) (string, bool, error) {
	start := fstabStartMarker(name)
	end := fstabEndMarker(name)
	startIndex := strings.Index(content, start)
	if startIndex == -1 {
		return "", false, nil
	}
	endIndex := strings.Index(content[startIndex:], end)
	if endIndex == -1 {
		return "", true, fmt.Errorf("managed fstab entry %q is missing end marker", name)
	}
	blockStart := startIndex + len(start)
	blockEnd := startIndex + endIndex
	return content[blockStart:blockEnd], true, nil
}

func formatFstabManagedBlock(entry FstabEntry) string {
	return fmt.Sprintf(
		"%s\n%s\t%s\t%s\t%s\t%d\t%d\n%s\n",
		fstabStartMarker(entry.Name),
		entry.Device,
		entry.MountPoint,
		entry.FSType,
		entry.Options,
		entry.Dump,
		entry.Pass,
		fstabEndMarker(entry.Name),
	)
}

func fstabStartMarker(name string) string {
	return fmt.Sprintf("# BEGIN %s %s", hostFstabMarkerID, name)
}

func fstabEndMarker(name string) string {
	return fmt.Sprintf("# END %s %s", hostFstabMarkerID, name)
}
