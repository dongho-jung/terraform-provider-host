package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	hostScheduleRuntimeDirName     = "schedules"
	hostScheduleCronMarkerPrefix   = "# terraform-provider-host schedule "
	hostSchedulePackageFedoraCron  = "cronie"
	hostScheduleCrondSystemdUnit   = "crond.service"
	hostScheduleCronNoCrontabToken = "no crontab"
	hostScheduleRootUser           = "root"
)

var hostScheduleIDPattern = regexp.MustCompile(`^[a-f0-9]{16}$`)

type ScheduleManager interface {
	UpsertSchedule(ctx context.Context, spec HostScheduleSpec) (HostScheduleStatus, error)
	ReadSchedule(ctx context.Context, spec HostScheduleSpec) (HostScheduleStatus, bool, error)
	DeleteSchedule(ctx context.Context, spec HostScheduleSpec) error
}

type HostScheduleSpec struct {
	ID               string            `json:"id"`
	User             string            `json:"user"`
	Command          string            `json:"command"`
	Schedule         string            `json:"schedule,omitempty"`
	Every            string            `json:"every,omitempty"`
	Shell            string            `json:"shell"`
	Enabled          bool              `json:"enabled"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	StdoutPath       string            `json:"stdout_path,omitempty"`
	StderrPath       string            `json:"stderr_path,omitempty"`
}

type HostScheduleStatus struct {
	ID                       string
	User                     string
	Backend                  string
	RuntimeDir               string
	ScriptPath               string
	WorkingDirectoryResolved string
	StdoutPathResolved       string
	StderrPathResolved       string
	RuntimeDrifted           bool
}

type CLICronScheduleManager struct {
	crontabPathMu  sync.Mutex
	crontabWriteMu sync.Mutex
	crontabPath    string
	packageManager PackageManager
	sudoPath       string
	homeDir        string
	runtimeDir     string
	targetUser     string
}

type CLICronScheduleManagerOptions struct {
	HomeDir    string
	RuntimeDir string
	TargetUser string
}

type hostScheduleMetadata struct {
	Spec       HostScheduleSpec `json:"spec"`
	Backend    string           `json:"backend"`
	ScriptPath string           `json:"script_path"`
}

func NewCLICronScheduleManager(crontabPath string, packageManager PackageManager, sudoPath string, options CLICronScheduleManagerOptions) *CLICronScheduleManager {
	return &CLICronScheduleManager{
		crontabPath:    crontabPath,
		packageManager: packageManager,
		sudoPath:       sudoPath,
		homeDir:        options.HomeDir,
		runtimeDir:     options.RuntimeDir,
		targetUser:     options.TargetUser,
	}
}

func newHostScheduleID() (string, error) {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate schedule ID: %w", err)
	}

	return hex.EncodeToString(bytes[:]), nil
}

func validateHostScheduleID(id string) error {
	if !hostScheduleIDPattern.MatchString(id) {
		return fmt.Errorf("schedule ID must be 16 lowercase hexadecimal characters")
	}

	return nil
}

func hostScheduleRuntimeDirForRuntime(id string, runtimeDir string) (string, error) {
	if err := validateHostScheduleID(id); err != nil {
		return "", err
	}

	scheduleDir, err := providerRuntimeSubdirForRuntime(runtimeDir, hostScheduleRuntimeDirName)
	if err != nil {
		return "", err
	}

	return filepath.Join(scheduleDir, id), nil
}

func hostScheduleScriptPathForRuntime(id string, runtimeDir string) (string, error) {
	resolvedRuntimeDir, err := hostScheduleRuntimeDirForRuntime(id, runtimeDir)
	if err != nil {
		return "", err
	}

	return filepath.Join(resolvedRuntimeDir, "run.sh"), nil
}

func hostScheduleMetadataPathForRuntime(id string, runtimeDir string) (string, error) {
	resolvedRuntimeDir, err := hostScheduleRuntimeDirForRuntime(id, runtimeDir)
	if err != nil {
		return "", err
	}

	return filepath.Join(resolvedRuntimeDir, "metadata.json"), nil
}

func (m *CLICronScheduleManager) UpsertSchedule(ctx context.Context, spec HostScheduleSpec) (HostScheduleStatus, error) {
	if err := applyHostScheduleTargetUser(&spec, m.targetUser); err != nil {
		return HostScheduleStatus{}, err
	}
	if err := validateHostScheduleSpecForHome(spec, m.homeDir); err != nil {
		return HostScheduleStatus{}, err
	}

	status, err := hostScheduleStatusForProvider(spec, m.homeDir, m.runtimeDir)
	if err != nil {
		return HostScheduleStatus{}, err
	}

	if err := writeHostScheduleRuntimeFilesForProvider(spec, status, m.homeDir, m.runtimeDir); err != nil {
		return HostScheduleStatus{}, err
	}

	if err := m.syncCronEntry(ctx, spec, status); err != nil {
		return HostScheduleStatus{}, err
	}

	return status, nil
}

func (m *CLICronScheduleManager) ReadSchedule(ctx context.Context, spec HostScheduleSpec) (HostScheduleStatus, bool, error) {
	if err := validateHostScheduleID(spec.ID); err != nil {
		return HostScheduleStatus{}, false, err
	}
	if err := applyHostScheduleTargetUser(&spec, m.targetUser); err != nil {
		return HostScheduleStatus{}, false, err
	}

	status, err := hostScheduleStatusForProvider(spec, m.homeDir, m.runtimeDir)
	if err != nil {
		return HostScheduleStatus{}, false, err
	}

	runtimeHealth, err := inspectHostScheduleRuntimeForProvider(spec, status, m.homeDir, m.runtimeDir)
	if err != nil {
		return HostScheduleStatus{}, false, err
	}

	cronPresent := false
	cronMatches := !spec.Enabled
	if m.resolveCrontabPath() {
		lines, err := m.readCrontab(ctx, spec.User)
		if err != nil {
			return HostScheduleStatus{}, false, err
		}
		cronPresent, cronMatches, err = inspectHostScheduleCronEntry(lines, spec, status)
		if err != nil {
			return HostScheduleStatus{}, false, err
		}
	}

	if !runtimeHealth.Present && !cronPresent {
		return status, false, nil
	}

	status.RuntimeDrifted = !runtimeHealth.Valid || !cronMatches

	return status, true, nil
}

func (m *CLICronScheduleManager) DeleteSchedule(ctx context.Context, spec HostScheduleSpec) error {
	if err := validateHostScheduleID(spec.ID); err != nil {
		return err
	}
	if err := applyHostScheduleTargetUser(&spec, m.targetUser); err != nil {
		return err
	}

	status, err := hostScheduleStatusForProvider(spec, m.homeDir, m.runtimeDir)
	if err != nil {
		return err
	}

	if err := m.removeCronEntry(ctx, spec, status); err != nil {
		return err
	}

	runtimeDir, err := hostScheduleRuntimeDirForRuntime(spec.ID, m.runtimeDir)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(runtimeDir); err != nil {
		return fmt.Errorf("remove schedule runtime directory %q: %w", runtimeDir, err)
	}

	return nil
}

func hostScheduleStatusForProvider(spec HostScheduleSpec, homeDir string, runtimeDir string) (HostScheduleStatus, error) {
	if err := validateHostScheduleID(spec.ID); err != nil {
		return HostScheduleStatus{}, err
	}
	if err := validateHostScheduleTargetUser(spec.User); err != nil {
		return HostScheduleStatus{}, err
	}

	scriptPath, err := hostScheduleScriptPathForRuntime(spec.ID, runtimeDir)
	if err != nil {
		return HostScheduleStatus{}, err
	}
	resolvedRuntimeDir, err := hostScheduleRuntimeDirForRuntime(spec.ID, runtimeDir)
	if err != nil {
		return HostScheduleStatus{}, err
	}
	workingDirectoryResolved, err := resolveOptionalHostSchedulePathForHome(spec.WorkingDirectory, homeDir)
	if err != nil {
		return HostScheduleStatus{}, fmt.Errorf("invalid working_directory: %w", err)
	}
	stdoutPathResolved, err := resolveOptionalHostSchedulePathForHome(spec.StdoutPath, homeDir)
	if err != nil {
		return HostScheduleStatus{}, fmt.Errorf("invalid stdout_path: %w", err)
	}
	stderrPathResolved, err := resolveOptionalHostSchedulePathForHome(spec.StderrPath, homeDir)
	if err != nil {
		return HostScheduleStatus{}, fmt.Errorf("invalid stderr_path: %w", err)
	}

	return HostScheduleStatus{
		ID:                       spec.ID,
		User:                     spec.User,
		Backend:                  "cron",
		RuntimeDir:               resolvedRuntimeDir,
		ScriptPath:               scriptPath,
		WorkingDirectoryResolved: workingDirectoryResolved,
		StdoutPathResolved:       stdoutPathResolved,
		StderrPathResolved:       stderrPathResolved,
	}, nil
}

func resolveOptionalHostSchedulePathForHome(path string, homeDir string) (string, error) {
	if path == "" {
		return "", nil
	}
	return expandHostPathWithHome(path, homeDir)
}

func writeHostScheduleRuntimeFilesForProvider(spec HostScheduleSpec, status HostScheduleStatus, homeDir string, runtimeDir string) error {
	resolvedRuntimeDir, err := hostScheduleRuntimeDirForRuntime(spec.ID, runtimeDir)
	if err != nil {
		return err
	}
	dirMode, scriptMode, err := hostScheduleRuntimeModes(spec)
	if err != nil {
		return err
	}
	if err := ensureHostScheduleRuntimeDir(resolvedRuntimeDir, dirMode); err != nil {
		return fmt.Errorf("create schedule runtime directory %q: %w", resolvedRuntimeDir, err)
	}
	if err := chmodHostScheduleRuntimeDirsForRuntime(spec, resolvedRuntimeDir, dirMode, runtimeDir); err != nil {
		return err
	}

	script, err := renderHostScheduleScriptForHome(spec, homeDir)
	if err != nil {
		return err
	}
	if err := writeHostScheduleFileAtomically(status.ScriptPath, []byte(script), scriptMode); err != nil {
		return fmt.Errorf("write schedule script %q: %w", status.ScriptPath, err)
	}

	metadataBytes, err := renderHostScheduleMetadata(spec, status)
	if err != nil {
		return err
	}
	metadataPath, err := hostScheduleMetadataPathForRuntime(spec.ID, runtimeDir)
	if err != nil {
		return err
	}
	if err := writeHostScheduleFileAtomically(metadataPath, metadataBytes, 0o600); err != nil {
		return fmt.Errorf("write schedule metadata %q: %w", metadataPath, err)
	}

	return nil
}

func ensureHostScheduleRuntimeDir(path string, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		// The schedule ID directory is provider-owned. Remove only the leaf
		// entry (never a followed symlink target) before recreating it.
		if err := os.Remove(path); err != nil {
			return err
		}
	}

	return os.MkdirAll(path, mode)
}

func hostScheduleRuntimeModes(spec HostScheduleSpec) (os.FileMode, os.FileMode, error) {
	shared, err := hostScheduleNeedsSharedRuntime(spec)
	if err != nil {
		return 0, 0, err
	}
	if shared {
		return 0o755, 0o755, nil
	}

	return 0o700, 0o700, nil
}

func hostScheduleNeedsSharedRuntime(spec HostScheduleSpec) (bool, error) {
	currentUser, err := currentHostUsername()
	if err != nil {
		return false, err
	}

	return spec.User != "" && spec.User != currentUser && spec.User != hostScheduleRootUser, nil
}

func chmodHostScheduleRuntimeDirsForRuntime(spec HostScheduleSpec, runtimeDir string, mode os.FileMode, configuredRuntimeDir string) error {
	if err := os.Chmod(runtimeDir, mode); err != nil {
		return fmt.Errorf("chmod schedule runtime directory %q: %w", runtimeDir, err)
	}

	shared, err := hostScheduleNeedsSharedRuntime(spec)
	if err != nil {
		return err
	}

	if !shared {
		return nil
	}

	runtimeRoot, err := providerRuntimeDirForRuntime(configuredRuntimeDir)
	if err != nil {
		return err
	}
	schedulesRoot, err := providerRuntimeSubdirForRuntime(configuredRuntimeDir, hostScheduleRuntimeDirName)
	if err != nil {
		return err
	}
	for _, path := range []string{runtimeRoot, schedulesRoot} {
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("chmod schedule runtime directory %q: %w", path, err)
		}
	}

	return nil
}

func (m *CLICronScheduleManager) syncCronEntry(ctx context.Context, spec HostScheduleSpec, status HostScheduleStatus) error {
	// crontab has a whole-file replace API. Serialize the read/modify/write
	// transaction so parallel schedule resources cannot overwrite each other.
	m.crontabWriteMu.Lock()
	defer m.crontabWriteMu.Unlock()

	if err := m.ensureCrontab(ctx); err != nil {
		return err
	}

	lines, err := m.readCrontab(ctx, spec.User)
	if err != nil {
		return err
	}

	next := filterHostScheduleCronEntry(lines, spec.ID, status.ScriptPath)
	if spec.Enabled {
		entry, err := renderHostScheduleCronEntry(spec, status)
		if err != nil {
			return err
		}
		next = append(next, entry...)
	}

	if stringSlicesEqual(lines, next) {
		return nil
	}

	return m.writeCrontab(ctx, spec.User, next)
}

func (m *CLICronScheduleManager) removeCronEntry(ctx context.Context, spec HostScheduleSpec, status HostScheduleStatus) error {
	m.crontabWriteMu.Lock()
	defer m.crontabWriteMu.Unlock()

	if !m.resolveCrontabPath() {
		return nil
	}

	lines, err := m.readCrontab(ctx, spec.User)
	if err != nil {
		return err
	}

	next := filterHostScheduleCronEntry(lines, spec.ID, status.ScriptPath)
	if stringSlicesEqual(lines, next) {
		return nil
	}

	return m.writeCrontab(ctx, spec.User, next)
}

func (m *CLICronScheduleManager) ensureCrontab(ctx context.Context) error {
	if m.resolveCrontabPath() {
		return nil
	}

	if runtime.GOOS == "linux" && m.packageManager != nil {
		if err := m.packageManager.InstallPackages(ctx, []string{hostSchedulePackageFedoraCron}); err != nil {
			return fmt.Errorf("install cron dependency %q: %w", hostSchedulePackageFedoraCron, err)
		}
		if m.resolveCrontabPath() {
			_ = startCrondService(ctx)
			return nil
		}
	}

	return fmt.Errorf("crontab executable not found in PATH")
}

func (m *CLICronScheduleManager) resolveCrontabPath() bool {
	m.crontabPathMu.Lock()
	defer m.crontabPathMu.Unlock()

	if m.crontabPath != "" {
		return true
	}

	crontabPath, err := exec.LookPath("crontab")
	if err != nil {
		return false
	}
	m.crontabPath = crontabPath

	return true
}

func startCrondService(ctx context.Context) error {
	systemctlPath, _ := exec.LookPath("systemctl")
	if systemctlPath == "" {
		return nil
	}

	cmd := exec.CommandContext(ctx, systemctlPath, "enable", "--now", hostScheduleCrondSystemdUnit)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("start %s: %w\n%s", hostScheduleCrondSystemdUnit, err, strings.TrimSpace(stderr.String()))
	}

	return nil
}

func (m *CLICronScheduleManager) readCrontab(ctx context.Context, targetUser string) ([]string, error) {
	if !m.resolveCrontabPath() {
		return nil, fmt.Errorf("crontab executable not found in PATH")
	}

	cmd, display, err := m.crontabCommand(ctx, targetUser, "-l")
	if err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		output := strings.ToLower(strings.TrimSpace(stderr.String() + "\n" + stdout.String()))
		if strings.Contains(output, hostScheduleCronNoCrontabToken) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s failed: %w\n%s", display, err, strings.TrimSpace(stderr.String()))
	}

	return splitCronLines(stdout.String()), nil
}

func (m *CLICronScheduleManager) writeCrontab(ctx context.Context, targetUser string, lines []string) error {
	if !m.resolveCrontabPath() {
		return fmt.Errorf("crontab executable not found in PATH")
	}

	tempFile, err := os.CreateTemp("", "terraform-provider-host-crontab-*.txt")
	if err != nil {
		return fmt.Errorf("create temporary crontab: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if _, err := tempFile.WriteString(content); err != nil {
		tempFile.Close()
		return fmt.Errorf("write temporary crontab %q: %w", tempPath, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temporary crontab %q: %w", tempPath, err)
	}

	cmd, display, err := m.crontabCommand(ctx, targetUser, tempPath)
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w\n%s", display, err, strings.TrimSpace(stderr.String()))
	}

	return nil
}

func (m *CLICronScheduleManager) crontabCommand(ctx context.Context, targetUser string, args ...string) (*exec.Cmd, string, error) {
	currentUser, err := currentHostUsername()
	if err != nil {
		return nil, "", err
	}

	crontabArgs, targetsOtherUser := hostScheduleCrontabArgs(targetUser, currentUser, args...)
	commandName := m.crontabPath
	commandArgs := crontabArgs

	if targetsOtherUser && os.Geteuid() != 0 {
		if m.sudoPath == "" {
			return nil, "", fmt.Errorf("managing crontab for user %q requires root privileges, but sudo was not found in PATH", targetUser)
		}
		if err := m.authenticateSudo(ctx, m.crontabPath, crontabArgs...); err != nil {
			return nil, "", err
		}

		commandName = m.sudoPath
		commandArgs = append([]string{m.crontabPath}, crontabArgs...)
	}

	display := strings.TrimSpace(commandName + " " + strings.Join(commandArgs, " "))
	return exec.CommandContext(ctx, commandName, commandArgs...), display, nil
}

func hostScheduleCrontabArgs(targetUser string, currentUser string, args ...string) ([]string, bool) {
	if targetUser == "" || targetUser == currentUser {
		return args, false
	}

	withUser := append([]string{"-u", targetUser}, args...)
	return withUser, true
}

func (m *CLICronScheduleManager) authenticateSudo(ctx context.Context, name string, args ...string) error {
	check := exec.CommandContext(ctx, m.sudoPath, "-n", "-v")
	if err := check.Run(); err == nil {
		return nil
	}

	fmt.Fprintf(os.Stderr, "\nTerraform provider host needs sudo privileges for: %s %s\n", name, strings.Join(args, " "))
	fmt.Fprintln(os.Stderr, "Enter your sudo password at the prompt below, or run `sudo -v` before `terraform apply`.")

	cmd := exec.CommandContext(ctx, m.sudoPath, "-v")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo authentication failed: %w. Run `sudo -v` before `terraform apply`, or configure passwordless sudo for local schedule management", err)
	}

	return nil
}

func renderHostScheduleCronEntry(spec HostScheduleSpec, status HostScheduleStatus) ([]string, error) {
	expression, err := cronExpressionForSpec(spec)
	if err != nil {
		return nil, err
	}

	return []string{
		hostScheduleCronMarkerPrefix + spec.ID,
		expression + " " + shellQuote(status.ScriptPath),
	}, nil
}

func filterHostScheduleCronEntry(lines []string, id string, scriptPath string) []string {
	next := make([]string, 0, len(lines))
	marker := hostScheduleCronMarkerPrefix + id
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == marker {
			if i+1 < len(lines) {
				i++
			}
			continue
		}
		if (scriptPath != "" && strings.Contains(lines[i], scriptPath)) || hostScheduleCronLineReferencesID(lines[i], id) {
			continue
		}
		next = append(next, lines[i])
	}

	return next
}

func inspectHostScheduleCronEntry(lines []string, spec HostScheduleSpec, status HostScheduleStatus) (bool, bool, error) {
	expected, err := renderHostScheduleCronEntry(spec, status)
	if err != nil {
		return false, false, err
	}

	marker := hostScheduleCronMarkerPrefix + spec.ID
	markerCount := 0
	matchingEntryCount := 0
	scriptReferenceCount := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == marker {
			markerCount++
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == expected[1] {
				matchingEntryCount++
			}
		}
		if (status.ScriptPath != "" && strings.Contains(line, status.ScriptPath)) || hostScheduleCronLineReferencesID(line, spec.ID) {
			scriptReferenceCount++
		}
	}

	present := markerCount > 0 || scriptReferenceCount > 0
	if !spec.Enabled {
		return present, !present, nil
	}

	matches := markerCount == 1 && matchingEntryCount == 1 && scriptReferenceCount == 1
	return present, matches, nil
}

func hostScheduleCronLineReferencesID(line string, id string) bool {
	if validateHostScheduleID(id) != nil {
		return false
	}
	providerScriptSuffix := "/" + hostScheduleRuntimeDirName + "/" + id + "/run.sh"
	return strings.Contains(filepath.ToSlash(line), providerScriptSuffix)
}

func splitCronLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return nil
	}

	return strings.Split(content, "\n")
}

func validateHostScheduleSpecForHome(spec HostScheduleSpec, homeDir string) error {
	if err := validateHostScheduleID(spec.ID); err != nil {
		return err
	}
	if err := validateHostScheduleTargetUser(spec.User); err != nil {
		return err
	}

	if strings.TrimSpace(spec.Command) == "" {
		return fmt.Errorf("command must be non-empty")
	}
	if strings.Contains(spec.Command, "\x00") {
		return fmt.Errorf("command must not contain NUL bytes")
	}
	if spec.Schedule == "" && spec.Every == "" {
		return fmt.Errorf("one of `schedule` or `every` must be set")
	}
	if spec.Schedule != "" && spec.Every != "" {
		return fmt.Errorf("only one of `schedule` or `every` can be set")
	}
	if _, err := cronExpressionForSpec(spec); err != nil {
		return err
	}
	if err := validateScheduleShell(spec.Shell); err != nil {
		return err
	}
	if spec.WorkingDirectory != "" {
		if _, err := expandHostPathWithHome(spec.WorkingDirectory, homeDir); err != nil {
			return fmt.Errorf("invalid working_directory: %w", err)
		}
	}
	if spec.StdoutPath != "" {
		if _, err := expandHostPathWithHome(spec.StdoutPath, homeDir); err != nil {
			return fmt.Errorf("invalid stdout_path: %w", err)
		}
	}
	if spec.StderrPath != "" {
		if _, err := expandHostPathWithHome(spec.StderrPath, homeDir); err != nil {
			return fmt.Errorf("invalid stderr_path: %w", err)
		}
	}
	for key, value := range spec.Environment {
		if strings.TrimSpace(key) != key || key == "" {
			return fmt.Errorf("environment variable names must be non-empty and must not contain leading or trailing whitespace")
		}
		if strings.ContainsAny(key, "\x00=") || strings.ContainsAny(value, "\x00") {
			return fmt.Errorf("environment variables must not contain NUL bytes, and names must not contain `=`")
		}
	}

	return nil
}

func applyHostScheduleTargetUser(spec *HostScheduleSpec, targetUser string) error {
	targetUser = strings.TrimSpace(targetUser)
	if err := validateHostScheduleTargetUser(targetUser); err != nil {
		return err
	}
	spec.User = targetUser
	return nil
}

func validateHostScheduleTargetUser(targetUser string) error {
	targetUser = strings.TrimSpace(targetUser)
	if targetUser == "" {
		return fmt.Errorf("target user must be configured on the provider")
	}
	return validateHostUserName(targetUser)
}

func currentHostUsername() (string, error) {
	current, err := osuser.Current()
	if err != nil {
		return "", fmt.Errorf("resolve current user: %w", err)
	}
	if strings.TrimSpace(current.Username) == "" {
		return "", fmt.Errorf("resolve current user: username is empty")
	}

	username := current.Username
	if before, after, ok := strings.Cut(username, "\\"); ok && before != "" && after != "" {
		username = after
	}

	return username, nil
}

func validateScheduleShell(shell string) error {
	if shell == "" {
		return fmt.Errorf("shell must be non-empty")
	}
	if strings.TrimSpace(shell) != shell {
		return fmt.Errorf("shell must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(shell, "\r\n\x00") {
		return fmt.Errorf("shell must not contain control characters")
	}
	if !filepath.IsAbs(shell) {
		return fmt.Errorf("shell must be an absolute path")
	}

	return nil
}

func cronExpressionForSpec(spec HostScheduleSpec) (string, error) {
	if spec.Schedule != "" {
		expression := strings.TrimSpace(spec.Schedule)
		if err := validateCronExpression(expression); err != nil {
			return "", err
		}
		return expression, nil
	}

	return cronExpressionFromEvery(spec.Every)
}

func validateCronExpression(expression string) error {
	switch expression {
	case "@hourly", "@daily", "@midnight", "@weekly", "@monthly", "@yearly", "@annually":
		return nil
	}

	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return fmt.Errorf("schedule must be a five-field cron expression or one of @hourly, @daily, @weekly, @monthly, @yearly")
	}

	validators := []struct {
		label           string
		minimum         int
		maximum         int
		normalizeSunday bool
	}{
		{label: "minute", minimum: 0, maximum: 59},
		{label: "hour", minimum: 0, maximum: 23},
		{label: "day-of-month", minimum: 1, maximum: 31},
		{label: "month", minimum: 1, maximum: 12},
		{label: "day-of-week", minimum: 0, maximum: 7, normalizeSunday: true},
	}
	for i, validator := range validators {
		if err := validateCronNumberField(fields[i], validator.minimum, validator.maximum, validator.normalizeSunday); err != nil {
			return fmt.Errorf("invalid %s field: %w", validator.label, err)
		}
	}

	return nil
}

func validateCronNumberField(field string, minimum int, maximum int, normalizeSunday bool) error {
	field = strings.TrimSpace(field)
	if field == "" {
		return fmt.Errorf("field is empty")
	}

	for _, part := range strings.Split(field, ",") {
		if err := validateCronNumberPart(strings.TrimSpace(part), minimum, maximum, normalizeSunday); err != nil {
			return err
		}
	}

	return nil
}

func validateCronNumberPart(part string, minimum int, maximum int, normalizeSunday bool) error {
	if part == "" {
		return fmt.Errorf("empty list item")
	}

	rangePart := part
	if before, after, ok := strings.Cut(part, "/"); ok {
		rangePart = before
		parsedStep, err := strconv.Atoi(after)
		if err != nil || parsedStep <= 0 {
			return fmt.Errorf("invalid step %q", after)
		}
	}

	start := minimum
	end := maximum
	if rangePart != "*" {
		if before, after, ok := strings.Cut(rangePart, "-"); ok {
			parsedStart, err := strconv.Atoi(before)
			if err != nil {
				return fmt.Errorf("invalid range start %q", before)
			}
			parsedEnd, err := strconv.Atoi(after)
			if err != nil {
				return fmt.Errorf("invalid range end %q", after)
			}
			start = parsedStart
			end = parsedEnd
		} else {
			value, err := strconv.Atoi(rangePart)
			if err != nil {
				return fmt.Errorf("invalid value %q", rangePart)
			}
			start = value
			end = value
		}
	}

	if start > end {
		return fmt.Errorf("range start %d is greater than end %d", start, end)
	}
	if start < minimum || end > maximum {
		return fmt.Errorf("value range %d-%d is outside %d-%d", start, end, minimum, maximum)
	}
	if normalizeSunday && start == 7 && end == 7 {
		return nil
	}

	return nil
}

func cronExpressionFromEvery(value string) (string, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return "", fmt.Errorf("invalid every duration %q: %w", value, err)
	}
	if duration < time.Minute {
		return "", fmt.Errorf("every duration must be at least one minute for the cron backend")
	}
	if duration%time.Minute != 0 {
		return "", fmt.Errorf("every duration must be a whole number of minutes for the cron backend")
	}

	minutes := int(duration / time.Minute)
	switch {
	case minutes < 60:
		if 60%minutes != 0 {
			return "", fmt.Errorf("every duration %q cannot be represented exactly by cron; use `schedule` instead", value)
		}
		if minutes == 1 {
			return "* * * * *", nil
		}
		return fmt.Sprintf("*/%d * * * *", minutes), nil
	case minutes == 60:
		return "0 * * * *", nil
	case minutes < 24*60 && minutes%60 == 0:
		hours := minutes / 60
		if 24%hours != 0 {
			return "", fmt.Errorf("every duration %q cannot be represented exactly by cron; use `schedule` instead", value)
		}
		return fmt.Sprintf("0 */%d * * *", hours), nil
	case minutes == 24*60:
		return "0 0 * * *", nil
	default:
		return "", fmt.Errorf("every duration %q cannot be represented exactly by cron; use `schedule` instead", value)
	}
}

func renderHostScheduleScript(spec HostScheduleSpec) (string, error) {
	return renderHostScheduleScriptForHome(spec, "")
}

func renderHostScheduleScriptForHome(spec HostScheduleSpec, homeDir string) (string, error) {
	var builder strings.Builder
	builder.WriteString("#!")
	builder.WriteString(spec.Shell)
	builder.WriteString("\n")

	if spec.StdoutPath != "" {
		path, err := expandHostPathWithHome(spec.StdoutPath, homeDir)
		if err != nil {
			return "", fmt.Errorf("invalid stdout_path: %w", err)
		}
		builder.WriteString("exec >> ")
		builder.WriteString(shellQuote(path))
		builder.WriteString("\n")
	}
	if spec.StderrPath != "" {
		path, err := expandHostPathWithHome(spec.StderrPath, homeDir)
		if err != nil {
			return "", fmt.Errorf("invalid stderr_path: %w", err)
		}
		builder.WriteString("exec 2>> ")
		builder.WriteString(shellQuote(path))
		builder.WriteString("\n")
	}
	if len(spec.Environment) > 0 {
		for _, key := range sortedStringKeys(spec.Environment) {
			builder.WriteString("export ")
			builder.WriteString(key)
			builder.WriteString("=")
			builder.WriteString(shellQuote(spec.Environment[key]))
			builder.WriteString("\n")
		}
	}
	if spec.WorkingDirectory != "" {
		path, err := expandHostPathWithHome(spec.WorkingDirectory, homeDir)
		if err != nil {
			return "", fmt.Errorf("invalid working_directory: %w", err)
		}
		builder.WriteString("cd ")
		builder.WriteString(shellQuote(path))
		builder.WriteString("\n")
	}

	command := strings.TrimSpace(spec.Command)
	builder.WriteString(command)
	builder.WriteString("\n")

	return builder.String(), nil
}

type hostScheduleRuntimeHealth struct {
	Present bool
	Valid   bool
}

func inspectHostScheduleRuntimeForProvider(spec HostScheduleSpec, status HostScheduleStatus, homeDir string, runtimeDir string) (hostScheduleRuntimeHealth, error) {
	dirMode, scriptMode, err := hostScheduleRuntimeModes(spec)
	if err != nil {
		return hostScheduleRuntimeHealth{}, err
	}

	directoryInfo, err := os.Lstat(status.RuntimeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return hostScheduleRuntimeHealth{}, nil
		}
		return hostScheduleRuntimeHealth{}, fmt.Errorf("inspect schedule runtime directory %q: %w", status.RuntimeDir, err)
	}
	health := hostScheduleRuntimeHealth{Present: true}
	if !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 || directoryInfo.Mode().Perm() != dirMode.Perm() {
		return health, nil
	}

	expectedScript, err := renderHostScheduleScriptForHome(spec, homeDir)
	if err != nil {
		return health, err
	}
	scriptValid, err := hostScheduleFileMatches(status.ScriptPath, []byte(expectedScript), scriptMode)
	if err != nil {
		return health, err
	}

	expectedMetadata, err := renderHostScheduleMetadata(spec, status)
	if err != nil {
		return health, err
	}
	metadataPath, err := hostScheduleMetadataPathForRuntime(spec.ID, runtimeDir)
	if err != nil {
		return health, err
	}
	metadataValid, err := hostScheduleFileMatches(metadataPath, expectedMetadata, 0o600)
	if err != nil {
		return health, err
	}

	health.Valid = scriptValid && metadataValid
	return health, nil
}

func hostScheduleFileMatches(path string, expected []byte, mode os.FileMode) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect schedule runtime file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode.Perm() {
		return false, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read schedule runtime file %q: %w", path, err)
	}

	return bytes.Equal(content, expected), nil
}

func renderHostScheduleMetadata(spec HostScheduleSpec, status HostScheduleStatus) ([]byte, error) {
	metadata := hostScheduleMetadata{
		Spec:       spec,
		Backend:    "cron",
		ScriptPath: status.ScriptPath,
	}
	metadataBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode schedule metadata: %w", err)
	}

	return append(metadataBytes, '\n'), nil
}

func writeHostScheduleFileAtomically(path string, content []byte, mode os.FileMode) (returnErr error) {
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".terraform-provider-host-*")
	if err != nil {
		return fmt.Errorf("create temporary runtime file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) && returnErr == nil {
			returnErr = fmt.Errorf("remove temporary runtime file %q: %w", tempPath, err)
		}
	}()

	if err := tempFile.Chmod(mode); err != nil {
		return fmt.Errorf("chmod temporary runtime file %q: %w", tempPath, err)
	}
	if _, err := tempFile.Write(content); err != nil {
		return fmt.Errorf("write temporary runtime file %q: %w", tempPath, err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("sync temporary runtime file %q: %w", tempPath, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temporary runtime file %q: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace runtime file %q: %w", path, err)
	}

	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

func stringSlicesEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
