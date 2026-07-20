package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// HostSystemFileSpec describes a regular file installed with system ownership.
// Content is deliberately kept out of HostSystemFileStatus so source-backed
// resources only persist checksums in Terraform state.
type HostSystemFileSpec struct {
	Destination string
	Content     []byte
	Mode        os.FileMode
	Owner       string
	Group       string
}

type HostSystemFileStatus struct {
	Destination    string
	ChecksumSHA256 string
	Mode           os.FileMode
	Owner          string
	Group          string
}

func hostSystemRootGroup() string {
	return hostSystemRootGroupForOS(runtime.GOOS)
}

func hostSystemRootGroupForOS(goos string) string {
	switch goos {
	case "darwin", "freebsd":
		return "wheel"
	default:
		return "root"
	}
}

type HostSystemFileManager interface {
	File(context.Context, string) (HostSystemFileStatus, bool, error)
	InstallFile(context.Context, HostSystemFileSpec) (HostSystemFileStatus, error)
	DeleteFile(context.Context, string, string) error
	NeedsPrivilegeEscalation() bool
}

type hostSystemFileCommandResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type hostSystemFileCommandRunner interface {
	Run(context.Context, io.Reader, string, ...string) (hostSystemFileCommandResult, error)
	NeedsPrivilegeEscalation() bool
}

type hostRootCommandRunner struct {
	sudoPath          string
	resolveExecutable func(string) (string, error)
}

func newHostRootCommandRunner(sudoPath string) *hostRootCommandRunner {
	return &hostRootCommandRunner{
		sudoPath:          sudoPath,
		resolveExecutable: trustedHostSystemExecutable,
	}
}

func (r *hostRootCommandRunner) Run(ctx context.Context, stdin io.Reader, name string, args ...string) (hostSystemFileCommandResult, error) {
	resolver := r.resolveExecutable
	if resolver == nil {
		resolver = trustedHostSystemExecutable
	}
	resolvedName, err := resolver(name)
	if err != nil {
		return hostSystemFileCommandResult{}, err
	}

	commandName := resolvedName
	commandArgs := args
	if r.NeedsPrivilegeEscalation() {
		sudoPath := r.sudoPath
		if sudoPath == "" {
			resolvedSudoPath, resolveErr := resolver("sudo")
			if resolveErr != nil {
				return hostSystemFileCommandResult{}, fmt.Errorf("managing system files requires root privileges, but sudo was not found in a trusted system directory: %w", resolveErr)
			}
			sudoPath = resolvedSudoPath
		} else if !filepath.IsAbs(sudoPath) {
			return hostSystemFileCommandResult{}, fmt.Errorf("configured sudo path must be absolute, got %q", sudoPath)
		}
		if sudoPath == "" {
			return hostSystemFileCommandResult{}, fmt.Errorf("managing system files requires root privileges, but sudo was not found in a trusted system directory")
		}
		if err := authenticateHostSystemSudo(ctx, sudoPath, resolvedName, args...); err != nil {
			return hostSystemFileCommandResult{}, err
		}
		commandName = sudoPath
		commandArgs = append([]string{"-n", "--", resolvedName}, args...)
	}

	cmd := exec.CommandContext(ctx, commandName, commandArgs...)
	cmd.Stdin = stdin
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	result := hostSystemFileCommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, fmt.Errorf("start %s %s: %w", commandName, strings.Join(commandArgs, " "), err)
}

var hostSystemTrustedExecutableNames = map[string]struct{}{
	"cat":     {},
	"dd":      {},
	"install": {},
	"mkdir":   {},
	"mv":      {},
	"rm":      {},
	"stat":    {},
	"sudo":    {},
	"test":    {},
	"visudo":  {},
}

// trustedHostSystemExecutable deliberately ignores the caller's PATH. These
// programs can be passed to sudo, so resolving an attacker-controlled binary
// from a writable PATH entry would turn ordinary provider configuration into
// root code execution.
func trustedHostSystemExecutable(name string) (string, error) {
	if _, ok := hostSystemTrustedExecutableNames[name]; !ok {
		return "", fmt.Errorf("host system utility %q is not in the trusted allowlist", name)
	}
	for _, directory := range []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin", "/usr/local/bin", "/usr/local/sbin"} {
		candidate := filepath.Join(directory, name)
		if err := validateHostSystemFileProtectedParents(candidate); err != nil {
			continue
		}
		resolvedCandidate, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("resolve trusted host system utility %q: %w", candidate, err)
		}
		if !filepath.IsAbs(resolvedCandidate) {
			continue
		}
		resolvedCandidate = filepath.Clean(resolvedCandidate)
		info, err := os.Lstat(resolvedCandidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("inspect trusted host system utility %q: %w", resolvedCandidate, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
			continue
		}
		uid, _, ownershipErr := hostSystemFileNumericOwnership(info)
		if ownershipErr != nil {
			return "", fmt.Errorf("inspect trusted host system utility ownership %q: %w", resolvedCandidate, ownershipErr)
		}
		if uid == "0" {
			if err := validateHostSystemFileProtectedParents(resolvedCandidate); err == nil {
				return resolvedCandidate, nil
			}
		}
	}
	return "", fmt.Errorf("host system utility %q was not found in trusted system directories", name)
}

func (r *hostRootCommandRunner) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

type CLIHostSystemFileManager struct {
	runner                  hostSystemFileCommandRunner
	goos                    string
	requireProtectedParents bool
}

func NewCLIHostSystemFileManager(sudoPath string) *CLIHostSystemFileManager {
	return &CLIHostSystemFileManager{
		runner:                  newHostRootCommandRunner(sudoPath),
		goos:                    runtime.GOOS,
		requireProtectedParents: true,
	}
}

func newCLIHostSystemFileManagerWithRunner(runner hostSystemFileCommandRunner, goos string) *CLIHostSystemFileManager {
	return &CLIHostSystemFileManager{runner: runner, goos: goos}
}

func (m *CLIHostSystemFileManager) File(ctx context.Context, destination string) (HostSystemFileStatus, bool, error) {
	if err := validateHostSystemFileDestination(destination); err != nil {
		return HostSystemFileStatus{}, false, err
	}
	if m.requireProtectedParents {
		if err := validateHostSystemFileProtectedParents(destination); err != nil {
			return HostSystemFileStatus{}, false, err
		}
	}

	status, exists, err := readHostSystemFileDirect(destination)
	if err == nil || os.IsNotExist(err) {
		return status, exists, nil
	}
	if !os.IsPermission(err) {
		return HostSystemFileStatus{}, false, err
	}
	return m.readFilePrivileged(ctx, destination)
}

func (m *CLIHostSystemFileManager) InstallFile(ctx context.Context, spec HostSystemFileSpec) (HostSystemFileStatus, error) {
	if m.requireProtectedParents {
		if err := validateHostSystemFileProtectedSpec(spec); err != nil {
			return HostSystemFileStatus{}, err
		}
	} else if err := validateHostSystemFileSpec(spec); err != nil {
		return HostSystemFileStatus{}, err
	}
	if err := validateHostSystemFileInstallPlatform(m.goos); err != nil {
		return HostSystemFileStatus{}, err
	}
	if m.requireProtectedParents {
		if err := validateHostSystemFileProtectedParents(spec.Destination); err != nil {
			return HostSystemFileStatus{}, err
		}
	}
	if _, _, err := m.File(ctx, spec.Destination); err != nil {
		return HostSystemFileStatus{}, err
	}

	if !m.requireProtectedParents {
		if err := m.runRequired(ctx, nil, "mkdir", "-p", filepath.Dir(spec.Destination)); err != nil {
			return HostSystemFileStatus{}, err
		}
	}

	// Both staging paths live under the protected destination parent. Create the
	// input as a root-owned regular file before streaming bytes into it so a
	// user-writable TMPDIR pathname is never reopened by a privileged process.
	// BSD install rejects pipe-backed /dev/stdin as a non-regular source, while
	// all supported Unix implementations special-case /dev/null, so the second
	// install copies from this prepared regular file portably.
	stagingTemp, err := hostSystemFileTargetTempPath(spec.Destination)
	if err != nil {
		return HostSystemFileStatus{}, err
	}
	defer m.removeTempFileIgnoringFailure(stagingTemp)
	targetTemp, err := hostSystemFileTargetTempPath(spec.Destination)
	if err != nil {
		return HostSystemFileStatus{}, err
	}
	defer m.removeTempFileIgnoringFailure(targetTemp)

	if err := m.runRequired(ctx, nil, "install", "-o", spec.Owner, "-g", spec.Group, "-m", "0600", "/dev/null", stagingTemp); err != nil {
		return HostSystemFileStatus{}, err
	}
	if err := m.runRequired(ctx, bytes.NewReader(spec.Content), "dd", "of="+stagingTemp, "bs=65536"); err != nil {
		return HostSystemFileStatus{}, err
	}
	if err := m.runRequired(ctx, nil, "install", "-o", spec.Owner, "-g", spec.Group, "-m", formatHostDirMode(spec.Mode), stagingTemp, targetTemp); err != nil {
		return HostSystemFileStatus{}, err
	}
	if err := m.runRequired(ctx, nil, "mv", "-f", targetTemp, spec.Destination); err != nil {
		return HostSystemFileStatus{}, err
	}

	status, exists, err := m.File(ctx, spec.Destination)
	if err != nil {
		return HostSystemFileStatus{}, err
	}
	if !exists {
		return HostSystemFileStatus{}, fmt.Errorf("system file %q was not found after install", spec.Destination)
	}
	if err := verifyHostSystemFileStatus(status, spec); err != nil {
		return HostSystemFileStatus{}, err
	}
	return status, nil
}

func validateHostSystemFileInstallPlatform(goos string) error {
	switch goos {
	case "linux", "darwin", "freebsd":
		return nil
	default:
		return fmt.Errorf("privileged system file installation is not supported on %s", goos)
	}
}

func (m *CLIHostSystemFileManager) DeleteFile(ctx context.Context, destination string, expectedChecksum string) error {
	if expectedChecksum == "" {
		return fmt.Errorf("refusing to delete system file %q without a deployed checksum", destination)
	}
	if m.requireProtectedParents {
		if err := validateHostSystemFileProtectedParents(destination); err != nil {
			return err
		}
	}
	status, exists, err := m.File(ctx, destination)
	if err != nil || !exists {
		return err
	}
	if status.ChecksumSHA256 != expectedChecksum {
		return fmt.Errorf("refusing to delete system file %q: current SHA256 %s does not match last deployed SHA256 %s", destination, status.ChecksumSHA256, expectedChecksum)
	}
	if m.requireProtectedParents {
		if err := validateHostSystemFileDeleteStatus(status); err != nil {
			return fmt.Errorf("refusing to delete system file %q: %w", destination, err)
		}
	}
	return m.runRequired(ctx, nil, "rm", "-f", destination)
}

func (m *CLIHostSystemFileManager) NeedsPrivilegeEscalation() bool {
	return m.runner != nil && m.runner.NeedsPrivilegeEscalation()
}

func (m *CLIHostSystemFileManager) readFilePrivileged(ctx context.Context, destination string) (HostSystemFileStatus, bool, error) {
	symlinkResult, err := m.runner.Run(ctx, nil, "test", "-L", destination)
	if err != nil {
		return HostSystemFileStatus{}, false, err
	}
	if symlinkResult.ExitCode == 0 {
		return HostSystemFileStatus{}, false, fmt.Errorf("system file destination %q is a symbolic link", destination)
	}
	if symlinkResult.ExitCode != 1 {
		return HostSystemFileStatus{}, false, hostSystemFileCommandError("test", []string{"-L", destination}, symlinkResult)
	}

	existsResult, err := m.runner.Run(ctx, nil, "test", "-e", destination)
	if err != nil {
		return HostSystemFileStatus{}, false, err
	}
	if existsResult.ExitCode == 1 {
		return HostSystemFileStatus{}, false, nil
	}
	if existsResult.ExitCode != 0 {
		return HostSystemFileStatus{}, false, hostSystemFileCommandError("test", []string{"-e", destination}, existsResult)
	}

	regularResult, err := m.runner.Run(ctx, nil, "test", "-f", destination)
	if err != nil {
		return HostSystemFileStatus{}, false, err
	}
	if regularResult.ExitCode != 0 {
		return HostSystemFileStatus{}, false, fmt.Errorf("system file destination %q exists and is not a regular file", destination)
	}

	statArgs := []string{"-c", "%a\t%u\t%g", destination}
	if m.goos == "darwin" || m.goos == "freebsd" {
		statArgs = []string{"-f", "%Lp\t%u\t%g", destination}
	} else if m.goos != "linux" {
		return HostSystemFileStatus{}, false, fmt.Errorf("privileged system file metadata is not supported on %s", m.goos)
	}
	statResult, err := m.runner.Run(ctx, nil, "stat", statArgs...)
	if err != nil {
		return HostSystemFileStatus{}, false, err
	}
	if statResult.ExitCode != 0 {
		return HostSystemFileStatus{}, false, hostSystemFileCommandError("stat", statArgs, statResult)
	}
	mode, owner, group, err := parseHostSystemFileStat(string(statResult.Stdout))
	if err != nil {
		return HostSystemFileStatus{}, false, err
	}
	contentResult, err := m.runner.Run(ctx, nil, "cat", destination)
	if err != nil {
		return HostSystemFileStatus{}, false, err
	}
	if contentResult.ExitCode != 0 {
		return HostSystemFileStatus{}, false, hostSystemFileCommandError("cat", []string{destination}, contentResult)
	}
	return HostSystemFileStatus{
		Destination:    destination,
		ChecksumSHA256: hostSystemFileChecksum(contentResult.Stdout),
		Mode:           mode,
		Owner:          owner,
		Group:          group,
	}, true, nil
}

func (m *CLIHostSystemFileManager) runRequired(ctx context.Context, stdin io.Reader, name string, args ...string) error {
	result, err := m.runner.Run(ctx, stdin, name, args...)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return hostSystemFileCommandError(name, args, result)
	}
	return nil
}

func (m *CLIHostSystemFileManager) runIgnoringFailure(ctx context.Context, name string, args ...string) {
	_, _ = m.runner.Run(ctx, nil, name, args...)
}

func (m *CLIHostSystemFileManager) removeTempFileIgnoringFailure(path string) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.runIgnoringFailure(cleanupCtx, "rm", "-f", path)
}

func readHostSystemFileDirect(destination string) (HostSystemFileStatus, bool, error) {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return HostSystemFileStatus{}, false, nil
	}
	if err != nil {
		return HostSystemFileStatus{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return HostSystemFileStatus{}, false, fmt.Errorf("system file destination %q is a symbolic link", destination)
	}
	if !info.Mode().IsRegular() {
		return HostSystemFileStatus{}, false, fmt.Errorf("system file destination %q exists and is not a regular file", destination)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		return HostSystemFileStatus{}, false, err
	}
	uid, gid, err := hostSystemFileNumericOwnership(info)
	if err != nil {
		return HostSystemFileStatus{}, false, err
	}
	return HostSystemFileStatus{
		Destination:    destination,
		ChecksumSHA256: hostSystemFileChecksum(content),
		Mode:           hostSystemFileObservedMode(info.Mode()),
		Owner:          hostSystemFileUserName(uid),
		Group:          hostSystemFileGroupName(gid),
	}, true, nil
}

func validateHostSystemFileSpec(spec HostSystemFileSpec) error {
	if err := validateHostSystemFileDestination(spec.Destination); err != nil {
		return err
	}
	if spec.Mode&^os.FileMode(0o777) != 0 {
		return fmt.Errorf("system file mode must contain only permission bits")
	}
	if err := validateHostUserName(spec.Owner); err != nil {
		return fmt.Errorf("invalid owner: %w", err)
	}
	if err := validateHostGroupName(spec.Group); err != nil {
		return fmt.Errorf("invalid group: %w", err)
	}
	return nil
}

func validateHostSystemFileProtectedSpec(spec HostSystemFileSpec) error {
	if err := validateHostSystemFileSpec(spec); err != nil {
		return err
	}
	if spec.Owner != "root" {
		return fmt.Errorf("protected system file owner must be root, got %q", spec.Owner)
	}
	if spec.Mode.Perm()&0o022 != 0 {
		return fmt.Errorf("protected system file mode %s must not be writable by group or other users", formatHostDirMode(spec.Mode))
	}
	group, err := user.LookupGroup(spec.Group)
	if err != nil {
		return fmt.Errorf("lookup protected system file group %q: %w", spec.Group, err)
	}
	canonical, err := user.LookupGroupId(group.Gid)
	if err != nil {
		return fmt.Errorf("resolve canonical protected system file group %q: %w", spec.Group, err)
	}
	if canonical.Name != spec.Group {
		return fmt.Errorf("protected system file group %q is not canonical; use %q", spec.Group, canonical.Name)
	}
	return nil
}

func validateHostSystemFileDeleteStatus(status HostSystemFileStatus) error {
	if status.Owner != "root" {
		return fmt.Errorf("current owner %q is not root", status.Owner)
	}
	if status.Mode.Perm()&0o022 != 0 {
		return fmt.Errorf("current mode %s is writable by group or other users", formatHostSystemFileMode(status.Mode))
	}
	if hostSystemFileSpecialMode(status.Mode) != 0 {
		return fmt.Errorf("current mode %s contains setuid, setgid, or sticky bits", formatHostSystemFileMode(status.Mode))
	}
	return nil
}

func validateHostSystemFileDestination(destination string) error {
	if strings.TrimSpace(destination) != destination || destination == "" {
		return fmt.Errorf("destination must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.Contains(destination, "\x00") {
		return fmt.Errorf("destination must not contain NUL bytes")
	}
	if strings.ContainsAny(destination, "\r\n") {
		return fmt.Errorf("destination must not contain line breaks")
	}
	if !filepath.IsAbs(destination) {
		return fmt.Errorf("destination must be an absolute path")
	}
	if filepath.Clean(destination) != destination {
		return fmt.Errorf("destination must be a clean absolute path, got %q", destination)
	}
	if destination == string(filepath.Separator) {
		return fmt.Errorf("destination must identify a file, not the filesystem root")
	}
	return nil
}

// validateHostSystemFileProtectedParents makes the separate checksum/read and
// atomic rename/unlink operations safe against an unprivileged directory-swap
// race. Every existing ancestor must be a real root-owned directory that is
// not writable by group or other users. The complete parent chain must already
// exist; this manager never creates privileged directories with an ambient
// umask.
func validateHostSystemFileProtectedParents(destination string) error {
	if err := validateHostSystemFileDestination(destination); err != nil {
		return err
	}
	for directory := filepath.Dir(destination); ; directory = filepath.Dir(directory) {
		info, err := os.Lstat(directory)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("system file parent %q does not exist; create the protected directory explicitly before managing files in it", directory)
			}
			return fmt.Errorf("inspect system file parent %q: %w", directory, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("system file parent %q must be a real directory and must not be a symbolic link", directory)
		}
		uid, _, err := hostSystemFileNumericOwnership(info)
		if err != nil {
			return fmt.Errorf("inspect system file parent ownership %q: %w", directory, err)
		}
		if uid != "0" || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("system file parent %q must be root-owned and not writable by group or other users", directory)
		}
		if directory == string(filepath.Separator) {
			break
		}
	}
	return nil
}

func verifyHostSystemFileStatus(status HostSystemFileStatus, spec HostSystemFileSpec) error {
	wantChecksum := hostSystemFileChecksum(spec.Content)
	if status.ChecksumSHA256 != wantChecksum {
		return fmt.Errorf("installed system file %q has SHA256 %s, want %s", spec.Destination, status.ChecksumSHA256, wantChecksum)
	}
	if hostSystemFileObservedMode(status.Mode) != hostSystemFileObservedMode(spec.Mode) {
		return fmt.Errorf("installed system file %q has mode %s, want %s", spec.Destination, formatHostSystemFileMode(status.Mode), formatHostSystemFileMode(spec.Mode))
	}
	if status.Owner != spec.Owner || status.Group != spec.Group {
		return fmt.Errorf("installed system file %q has owner %s:%s, want %s:%s", spec.Destination, status.Owner, status.Group, spec.Owner, spec.Group)
	}
	return nil
}

func hostSystemFileMatchesSpec(status HostSystemFileStatus, spec HostSystemFileSpec) bool {
	return status.ChecksumSHA256 == hostSystemFileChecksum(spec.Content) &&
		hostSystemFileObservedMode(status.Mode) == hostSystemFileObservedMode(spec.Mode) &&
		status.Owner == spec.Owner && status.Group == spec.Group
}

func hostSystemFileObservedMode(mode os.FileMode) os.FileMode {
	return mode & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
}

func hostSystemFileSpecialMode(mode os.FileMode) os.FileMode {
	return mode & (os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
}

func formatHostSystemFileMode(mode os.FileMode) string {
	value := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		value |= 0o4000
	}
	if mode&os.ModeSetgid != 0 {
		value |= 0o2000
	}
	if mode&os.ModeSticky != 0 {
		value |= 0o1000
	}
	return fmt.Sprintf("%04o", value)
}

func hostSystemFileChecksum(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func hostSystemFileTargetTempPath(destination string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate system file temporary name: %w", err)
	}
	return filepath.Join(filepath.Dir(destination), ".terraform-provider-host-"+hex.EncodeToString(random)+".tmp"), nil
}

func parseHostSystemFileStat(value string) (os.FileMode, string, string, error) {
	fields := strings.Split(strings.TrimSpace(value), "\t")
	if len(fields) != 3 {
		return 0, "", "", fmt.Errorf("parse system file stat output %q", value)
	}
	modeValue := strings.TrimSpace(fields[0])
	if modeValue == "" {
		return 0, "", "", fmt.Errorf("parse empty system file mode")
	}
	parsedMode, err := strconv.ParseUint(modeValue, 8, 16)
	if err != nil || parsedMode > 0o7777 {
		return 0, "", "", fmt.Errorf("parse system file mode %q as octal permissions", modeValue)
	}
	mode := os.FileMode(parsedMode & 0o777)
	if parsedMode&0o4000 != 0 {
		mode |= os.ModeSetuid
	}
	if parsedMode&0o2000 != 0 {
		mode |= os.ModeSetgid
	}
	if parsedMode&0o1000 != 0 {
		mode |= os.ModeSticky
	}
	uid := strings.TrimSpace(fields[1])
	gid := strings.TrimSpace(fields[2])
	if _, err := strconv.ParseUint(uid, 10, 32); err != nil {
		return 0, "", "", fmt.Errorf("parse system file uid %q: %w", uid, err)
	}
	if _, err := strconv.ParseUint(gid, 10, 32); err != nil {
		return 0, "", "", fmt.Errorf("parse system file gid %q: %w", gid, err)
	}
	return mode, hostSystemFileUserName(uid), hostSystemFileGroupName(gid), nil
}

func hostSystemFileUserName(uid string) string {
	entry, err := user.LookupId(uid)
	if err == nil && entry.Username != "" {
		return entry.Username
	}
	return "#" + uid
}

func hostSystemFileGroupName(gid string) string {
	entry, err := user.LookupGroupId(gid)
	if err == nil && entry.Name != "" {
		return entry.Name
	}
	return "#" + gid
}

func hostSystemFileCommandError(name string, args []string, result hostSystemFileCommandResult) error {
	detail := strings.TrimSpace(string(result.Stderr))
	if detail == "" {
		detail = strings.TrimSpace(string(result.Stdout))
	}
	if detail == "" {
		return fmt.Errorf("%s %s failed with exit code %d", name, strings.Join(args, " "), result.ExitCode)
	}
	return fmt.Errorf("%s %s failed with exit code %d: %s", name, strings.Join(args, " "), result.ExitCode, detail)
}
