package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type HostnameManager interface {
	Hostname(ctx context.Context) (string, error)
	SetHostname(ctx context.Context, name string) error
	NeedsPrivilegeEscalation() bool
}

type TimezoneManager interface {
	Timezone(ctx context.Context) (string, error)
	SetTimezone(ctx context.Context, name string) error
	NeedsPrivilegeEscalation() bool
}

type LocaleManager interface {
	Locale(ctx context.Context) (string, error)
	SetLocale(ctx context.Context, lang string) error
	NeedsPrivilegeEscalation() bool
}

type KeymapManager interface {
	Keymap(ctx context.Context) (string, error)
	SetKeymap(ctx context.Context, name string) error
	NeedsPrivilegeEscalation() bool
}

type SystemdServiceManager interface {
	ServiceStatus(ctx context.Context, name string) (SystemdServiceStatus, error)
	SyncService(ctx context.Context, spec SystemdServiceSpec) error
	NeedsPrivilegeEscalation() bool
}

type SystemdServiceStatus struct {
	Name    string
	Exists  bool
	Enabled bool
	Running bool
}

type SystemdServiceSpec struct {
	Name    string
	Enabled bool
	Running bool
}

type CLIHostnameManager struct {
	goos            string
	hostnamectlPath string
	scutilPath      string
	sudoPath        string
}

func NewCLIHostnameManager(goos string, hostnamectlPath string, scutilPath string, sudoPath string) *CLIHostnameManager {
	return &CLIHostnameManager{
		goos:            goos,
		hostnamectlPath: hostnamectlPath,
		scutilPath:      scutilPath,
		sudoPath:        sudoPath,
	}
}

func (m *CLIHostnameManager) Hostname(ctx context.Context) (string, error) {
	switch {
	case m.goos == "darwin" && m.scutilPath != "":
		out, err := m.run(ctx, false, m.scutilPath, "--get", "HostName")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	case m.hostnamectlPath != "":
		out, err := m.run(ctx, false, m.hostnamectlPath, "--static")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	default:
		return "", fmt.Errorf("hostname management requires hostnamectl or scutil")
	}
}

func (m *CLIHostnameManager) SetHostname(ctx context.Context, name string) error {
	if err := validateHostHostname(name); err != nil {
		return err
	}

	switch {
	case m.goos == "darwin" && m.scutilPath != "":
		_, err := m.run(ctx, true, m.scutilPath, "--set", "HostName", name)
		return err
	case m.hostnamectlPath != "":
		_, err := m.run(ctx, true, m.hostnamectlPath, "set-hostname", name)
		return err
	default:
		return fmt.Errorf("hostname management requires hostnamectl or scutil")
	}
}

func (m *CLIHostnameManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

func (m *CLIHostnameManager) run(ctx context.Context, mutate bool, name string, args ...string) ([]byte, error) {
	return runHostSystemCommand(ctx, mutate, m.sudoPath, "hostname", name, args...)
}

type CLITimezoneManager struct {
	goos            string
	timedatectlPath string
	systemsetupPath string
	sudoPath        string
}

func NewCLITimezoneManager(goos string, timedatectlPath string, systemsetupPath string, sudoPath string) *CLITimezoneManager {
	return &CLITimezoneManager{
		goos:            goos,
		timedatectlPath: timedatectlPath,
		systemsetupPath: systemsetupPath,
		sudoPath:        sudoPath,
	}
}

func (m *CLITimezoneManager) Timezone(ctx context.Context) (string, error) {
	switch {
	case m.goos == "darwin" && m.systemsetupPath != "":
		out, err := m.run(ctx, false, m.systemsetupPath, "-gettimezone")
		if err != nil {
			return "", err
		}
		return parseSystemSetupTimezone(string(out))
	case m.timedatectlPath != "":
		out, err := m.run(ctx, false, m.timedatectlPath, "show", "--property=Timezone", "--value")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	default:
		return "", fmt.Errorf("timezone management requires timedatectl or systemsetup")
	}
}

func (m *CLITimezoneManager) SetTimezone(ctx context.Context, name string) error {
	if err := validateHostTimezone(name); err != nil {
		return err
	}

	switch {
	case m.goos == "darwin" && m.systemsetupPath != "":
		_, err := m.run(ctx, true, m.systemsetupPath, "-settimezone", name)
		return err
	case m.timedatectlPath != "":
		_, err := m.run(ctx, true, m.timedatectlPath, "set-timezone", name)
		return err
	default:
		return fmt.Errorf("timezone management requires timedatectl or systemsetup")
	}
}

func (m *CLITimezoneManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

func (m *CLITimezoneManager) run(ctx context.Context, mutate bool, name string, args ...string) ([]byte, error) {
	return runHostSystemCommand(ctx, mutate, m.sudoPath, "timezone", name, args...)
}

type CLILocalectlManager struct {
	localectlPath string
	sudoPath      string
}

func NewCLILocalectlManager(localectlPath string, sudoPath string) *CLILocalectlManager {
	return &CLILocalectlManager{
		localectlPath: localectlPath,
		sudoPath:      sudoPath,
	}
}

func (m *CLILocalectlManager) Locale(ctx context.Context) (string, error) {
	out, err := m.run(ctx, false, m.localectlPath, "status")
	if err != nil {
		return "", err
	}
	return parseLocalectlLocale(string(out))
}

func (m *CLILocalectlManager) SetLocale(ctx context.Context, lang string) error {
	if err := validateHostLocale(lang); err != nil {
		return err
	}

	_, err := m.run(ctx, true, m.localectlPath, "set-locale", "LANG="+lang)
	return err
}

func (m *CLILocalectlManager) Keymap(ctx context.Context) (string, error) {
	out, err := m.run(ctx, false, m.localectlPath, "status")
	if err != nil {
		return "", err
	}
	return parseLocalectlKeymap(string(out))
}

func (m *CLILocalectlManager) SetKeymap(ctx context.Context, name string) error {
	if err := validateHostKeymap(name); err != nil {
		return err
	}

	_, err := m.run(ctx, true, m.localectlPath, "set-keymap", name)
	return err
}

func (m *CLILocalectlManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

func (m *CLILocalectlManager) run(ctx context.Context, mutate bool, name string, args ...string) ([]byte, error) {
	return runHostSystemCommand(ctx, mutate, m.sudoPath, "localectl", name, args...)
}

type CLISystemdServiceManager struct {
	systemctlPath string
	sudoPath      string
}

func NewCLISystemdServiceManager(systemctlPath string, sudoPath string) *CLISystemdServiceManager {
	return &CLISystemdServiceManager{
		systemctlPath: systemctlPath,
		sudoPath:      sudoPath,
	}
}

func (m *CLISystemdServiceManager) ServiceStatus(ctx context.Context, name string) (SystemdServiceStatus, error) {
	if err := validateSystemdServiceName(name); err != nil {
		return SystemdServiceStatus{}, err
	}

	loadOut, err := m.run(ctx, false, m.systemctlPath, "show", "--property=LoadState", "--value", name)
	if err != nil {
		return SystemdServiceStatus{}, err
	}
	loadState := strings.TrimSpace(string(loadOut))
	status := SystemdServiceStatus{
		Name:   name,
		Exists: loadState != "" && loadState != "not-found",
	}
	if !status.Exists {
		return status, nil
	}

	enabledOut, _ := m.run(ctx, false, m.systemctlPath, "is-enabled", name)
	status.Enabled = parseSystemdEnabled(string(enabledOut))

	activeOut, _ := m.run(ctx, false, m.systemctlPath, "is-active", name)
	status.Running = parseSystemdActive(string(activeOut))

	return status, nil
}

func (m *CLISystemdServiceManager) SyncService(ctx context.Context, spec SystemdServiceSpec) error {
	if err := validateSystemdServiceName(spec.Name); err != nil {
		return err
	}

	if spec.Enabled {
		if _, err := m.run(ctx, true, m.systemctlPath, "enable", spec.Name); err != nil {
			return err
		}
	} else {
		if _, err := m.run(ctx, true, m.systemctlPath, "disable", spec.Name); err != nil {
			return err
		}
	}

	if spec.Running {
		_, err := m.run(ctx, true, m.systemctlPath, "start", spec.Name)
		return err
	}

	_, err := m.run(ctx, true, m.systemctlPath, "stop", spec.Name)
	return err
}

func (m *CLISystemdServiceManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

func (m *CLISystemdServiceManager) run(ctx context.Context, mutate bool, name string, args ...string) ([]byte, error) {
	return runHostSystemCommand(ctx, mutate, m.sudoPath, "systemd", name, args...)
}

func runHostSystemCommand(ctx context.Context, mutate bool, sudoPath string, label string, name string, args ...string) ([]byte, error) {
	commandName := name
	commandArgs := args

	if mutate && os.Geteuid() != 0 {
		if sudoPath == "" {
			return nil, fmt.Errorf("mutating %s commands require root privileges, but sudo was not found in PATH", label)
		}
		if err := authenticateHostSystemSudo(ctx, sudoPath, name, args...); err != nil {
			return nil, err
		}

		commandName = sudoPath
		commandArgs = append([]string{name}, args...)
	}

	cmd := exec.CommandContext(ctx, commandName, commandArgs...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s failed: %w\n%s", commandName, strings.Join(commandArgs, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

func authenticateHostSystemSudo(ctx context.Context, sudoPath string, name string, args ...string) error {
	check := exec.CommandContext(ctx, sudoPath, "-n", "true")
	if err := check.Run(); err == nil {
		return nil
	}

	fmt.Fprintf(os.Stderr, "\nTerraform provider host needs sudo privileges for: %s %s\n", name, strings.Join(args, " "))
	fmt.Fprintln(os.Stderr, "Enter your sudo password at the prompt below, or run `sudo -v` before `terraform apply`.")

	cmd := exec.CommandContext(ctx, sudoPath, "-v")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo authentication failed: %w. Run `sudo -v` before `terraform apply`, or configure passwordless sudo for local system management", err)
	}

	return nil
}

func parseSystemSetupTimezone(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "Time Zone:"); ok {
			return strings.TrimSpace(value), nil
		}
	}
	return "", fmt.Errorf("unexpected systemsetup timezone output: %q", out)
}

func parseSystemdEnabled(out string) bool {
	return strings.TrimSpace(out) == "enabled"
}

func parseSystemdActive(out string) bool {
	return strings.TrimSpace(out) == "active"
}

func parseLocalectlLocale(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "System Locale:"); ok {
			for _, field := range strings.Fields(strings.TrimSpace(value)) {
				if lang, ok := strings.CutPrefix(field, "LANG="); ok {
					return strings.TrimSpace(lang), nil
				}
			}
		}
	}
	return "", fmt.Errorf("unexpected localectl locale output: %q", out)
}

func parseLocalectlKeymap(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "VC Keymap:"); ok {
			return strings.TrimSpace(value), nil
		}
	}
	return "", fmt.Errorf("unexpected localectl keymap output: %q", out)
}

func validateHostHostname(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("hostname must be non-empty and must not contain leading or trailing whitespace")
	}
	if len(name) > 253 {
		return fmt.Errorf("hostname must be at most 253 characters")
	}
	if strings.ContainsAny(name, " \t\r\n\x00") {
		return fmt.Errorf("hostname must not contain whitespace or NUL bytes")
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return fmt.Errorf("hostname must not contain empty labels")
		}
		if len(label) > 63 {
			return fmt.Errorf("hostname label %q must be at most 63 characters", label)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("hostname label %q must not start or end with '-'", label)
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return fmt.Errorf("hostname label %q contains invalid character %q", label, r)
		}
	}
	return nil
}

func validateHostTimezone(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("timezone must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(name, " \t\r\n\x00") {
		return fmt.Errorf("timezone must not contain whitespace or NUL bytes")
	}
	if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return fmt.Errorf("timezone must be a zoneinfo name such as America/Los_Angeles")
	}
	return nil
}

func validateHostLocale(lang string) error {
	if strings.TrimSpace(lang) != lang || lang == "" {
		return fmt.Errorf("locale must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(lang, " \t\r\n\x00") {
		return fmt.Errorf("locale must not contain whitespace or NUL bytes")
	}
	return nil
}

func validateHostKeymap(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("keymap must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(name, " \t\r\n\x00") {
		return fmt.Errorf("keymap must not contain whitespace or NUL bytes")
	}
	return nil
}

func validateSystemdUnitName(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("unit name must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(name, " \t\r\n/\x00") {
		return fmt.Errorf("unit name must not contain whitespace, '/', or NUL bytes")
	}
	for _, suffix := range []string{".service", ".socket", ".timer", ".target", ".path", ".mount", ".automount", ".slice"} {
		if strings.HasSuffix(name, suffix) {
			return nil
		}
	}
	return fmt.Errorf("unit name must end with a supported systemd unit suffix")
}

func validateSystemdServiceName(name string) error {
	if err := validateSystemdUnitName(name); err != nil {
		return fmt.Errorf("%s", strings.Replace(err.Error(), "unit", "service", 1))
	}
	if !strings.HasSuffix(name, ".service") {
		return fmt.Errorf("service name must end with .service")
	}
	return nil
}
