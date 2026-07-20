package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

type IdentityManager interface {
	GroupStatus(ctx context.Context, name string) (HostGroupStatus, bool, error)
	EnsureGroup(ctx context.Context, name string) error
	DeleteGroup(ctx context.Context, name string) error
	UserStatus(ctx context.Context, username string) (HostUserStatus, bool, error)
	UpsertUser(ctx context.Context, spec HostUserSpec, previousGroups []string) error
	DeleteUser(ctx context.Context, username string, removeHome bool) error
	NeedsPrivilegeEscalation() bool
}

type HostGroupStatus struct {
	Name string
	GID  string
}

type HostUserStatus struct {
	Username string
	UID      string
	GID      string
	FullName string
	Home     string
	Shell    string
	Groups   []string
}

type HostUserSpec struct {
	Username   string
	FullName   *string
	Home       *string
	Shell      *string
	CreateHome bool
	Groups     []string
}

type CLIIdentityManager struct {
	sudoPath string
	goos     string
}

func NewCLIIdentityManager(sudoPath string) *CLIIdentityManager {
	return &CLIIdentityManager{
		sudoPath: sudoPath,
		goos:     runtime.GOOS,
	}
}

func (m *CLIIdentityManager) GroupStatus(ctx context.Context, name string) (HostGroupStatus, bool, error) {
	if err := validateHostGroupName(name); err != nil {
		return HostGroupStatus{}, false, err
	}

	switch m.goos {
	case "darwin":
		return m.darwinGroupStatus(ctx, name)
	default:
		return m.unixGroupStatus(ctx, name)
	}
}

func (m *CLIIdentityManager) EnsureGroup(ctx context.Context, name string) error {
	if err := validateHostGroupName(name); err != nil {
		return err
	}

	_, exists, err := m.GroupStatus(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	switch m.goos {
	case "darwin":
		gid, err := m.nextDarwinID(ctx, "/Groups", "PrimaryGroupID", 500)
		if err != nil {
			return err
		}
		if _, err := m.run(ctx, true, "dscl", ".", "-create", "/Groups/"+name); err != nil {
			return err
		}
		_, err = m.run(ctx, true, "dscl", ".", "-create", "/Groups/"+name, "PrimaryGroupID", strconv.Itoa(gid))
		return err
	default:
		_, err := m.run(ctx, true, "groupadd", name)
		return err
	}
}

func (m *CLIIdentityManager) DeleteGroup(ctx context.Context, name string) error {
	if err := validateHostGroupName(name); err != nil {
		return err
	}

	_, exists, err := m.GroupStatus(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	switch m.goos {
	case "darwin":
		_, err := m.run(ctx, true, "dscl", ".", "-delete", "/Groups/"+name)
		return err
	default:
		_, err := m.run(ctx, true, "groupdel", name)
		return err
	}
}

func (m *CLIIdentityManager) UserStatus(ctx context.Context, username string) (HostUserStatus, bool, error) {
	if err := validateHostUserName(username); err != nil {
		return HostUserStatus{}, false, err
	}

	switch m.goos {
	case "darwin":
		return m.darwinUserStatus(ctx, username)
	default:
		return m.unixUserStatus(ctx, username)
	}
}

func (m *CLIIdentityManager) UpsertUser(ctx context.Context, spec HostUserSpec, previousGroups []string) error {
	if err := validateHostUserSpec(spec); err != nil {
		return err
	}

	_, exists, err := m.UserStatus(ctx, spec.Username)
	if err != nil {
		return err
	}

	for _, group := range spec.Groups {
		_, groupExists, err := m.GroupStatus(ctx, group)
		if err != nil {
			return err
		}
		if !groupExists {
			return fmt.Errorf("group %q does not exist", group)
		}
	}

	switch m.goos {
	case "darwin":
		if !exists {
			if err := m.createDarwinUser(ctx, spec); err != nil {
				return err
			}
		} else if err := m.updateDarwinUser(ctx, spec); err != nil {
			return err
		}
	default:
		if !exists {
			if err := m.createUnixUser(ctx, spec); err != nil {
				return err
			}
		} else if err := m.updateUnixUser(ctx, spec); err != nil {
			return err
		}
	}

	return m.syncUserGroups(ctx, spec.Username, spec.Groups, previousGroups)
}

func (m *CLIIdentityManager) DeleteUser(ctx context.Context, username string, removeHome bool) error {
	if err := validateHostUserName(username); err != nil {
		return err
	}

	status, exists, err := m.UserStatus(ctx, username)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	switch m.goos {
	case "darwin":
		if _, err := m.run(ctx, true, "dscl", ".", "-delete", "/Users/"+username); err != nil {
			return err
		}
		if removeHome && status.Home != "" {
			_, err := m.run(ctx, true, "rm", "-rf", status.Home)
			return err
		}
		return nil
	default:
		args := []string{}
		if removeHome {
			args = append(args, "-r")
		}
		args = append(args, username)
		_, err := m.run(ctx, true, "userdel", args...)
		return err
	}
}

func (m *CLIIdentityManager) NeedsPrivilegeEscalation() bool {
	return os.Geteuid() != 0
}

func (m *CLIIdentityManager) unixGroupStatus(ctx context.Context, name string) (HostGroupStatus, bool, error) {
	out, found, err := m.runOptional(ctx, "getent", "group", name)
	if err != nil {
		return HostGroupStatus{}, false, err
	}
	if !found {
		return HostGroupStatus{}, false, nil
	}

	status, err := parseGetentGroup(strings.TrimSpace(string(out)))
	if err != nil {
		return HostGroupStatus{}, false, err
	}

	return status, true, nil
}

func (m *CLIIdentityManager) unixUserStatus(ctx context.Context, username string) (HostUserStatus, bool, error) {
	out, found, err := m.runOptional(ctx, "getent", "passwd", username)
	if err != nil {
		return HostUserStatus{}, false, err
	}
	if !found {
		return HostUserStatus{}, false, nil
	}

	status, err := parseGetentPasswd(strings.TrimSpace(string(out)))
	if err != nil {
		return HostUserStatus{}, false, err
	}

	groups, err := m.userGroupNames(ctx, username)
	if err != nil {
		return HostUserStatus{}, false, err
	}
	status.Groups = groups

	return status, true, nil
}

func (m *CLIIdentityManager) darwinGroupStatus(ctx context.Context, name string) (HostGroupStatus, bool, error) {
	out, found, err := m.runOptional(ctx, "dscl", ".", "-read", "/Groups/"+name, "PrimaryGroupID")
	if err != nil {
		return HostGroupStatus{}, false, err
	}
	if !found {
		return HostGroupStatus{}, false, nil
	}

	gid, err := parseDSCLScalarAttribute(string(out), "PrimaryGroupID")
	if err != nil {
		return HostGroupStatus{}, false, err
	}

	return HostGroupStatus{Name: name, GID: gid}, true, nil
}

func (m *CLIIdentityManager) darwinUserStatus(ctx context.Context, username string) (HostUserStatus, bool, error) {
	_, found, err := m.runOptional(ctx, "dscl", ".", "-read", "/Users/"+username, "UniqueID")
	if err != nil {
		return HostUserStatus{}, false, err
	}
	if !found {
		return HostUserStatus{}, false, nil
	}

	status := HostUserStatus{Username: username}
	if status.UID, err = m.darwinUserScalar(ctx, username, "UniqueID"); err != nil {
		return HostUserStatus{}, false, err
	}
	if status.GID, err = m.darwinUserScalar(ctx, username, "PrimaryGroupID"); err != nil {
		return HostUserStatus{}, false, err
	}
	status.FullName, _ = m.darwinUserScalar(ctx, username, "RealName")
	status.Home, _ = m.darwinUserScalar(ctx, username, "NFSHomeDirectory")
	status.Shell, _ = m.darwinUserScalar(ctx, username, "UserShell")

	groups, err := m.userGroupNames(ctx, username)
	if err != nil {
		return HostUserStatus{}, false, err
	}
	status.Groups = groups

	return status, true, nil
}

func (m *CLIIdentityManager) darwinUserScalar(ctx context.Context, username string, key string) (string, error) {
	out, found, err := m.runOptional(ctx, "dscl", ".", "-read", "/Users/"+username, key)
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}

	return parseDSCLScalarAttribute(string(out), key)
}

func (m *CLIIdentityManager) createUnixUser(ctx context.Context, spec HostUserSpec) error {
	args := []string{}
	if spec.CreateHome {
		args = append(args, "--create-home")
	} else {
		args = append(args, "--no-create-home")
	}
	if spec.Home != nil {
		args = append(args, "--home-dir", *spec.Home)
	}
	if spec.Shell != nil {
		args = append(args, "--shell", *spec.Shell)
	}
	if spec.FullName != nil {
		args = append(args, "--comment", *spec.FullName)
	}
	args = append(args, spec.Username)

	_, err := m.run(ctx, true, "useradd", args...)
	return err
}

func (m *CLIIdentityManager) updateUnixUser(ctx context.Context, spec HostUserSpec) error {
	args := []string{}
	if spec.Home != nil {
		args = append(args, "--home", *spec.Home)
		if spec.CreateHome {
			args = append(args, "--move-home")
		}
	}
	if spec.Shell != nil {
		args = append(args, "--shell", *spec.Shell)
	}
	if spec.FullName != nil {
		args = append(args, "--comment", *spec.FullName)
	}
	if len(args) == 0 {
		return nil
	}

	args = append(args, spec.Username)
	_, err := m.run(ctx, true, "usermod", args...)
	return err
}

func (m *CLIIdentityManager) createDarwinUser(ctx context.Context, spec HostUserSpec) error {
	uid, err := m.nextDarwinID(ctx, "/Users", "UniqueID", 501)
	if err != nil {
		return err
	}

	home := "/Users/" + spec.Username
	if spec.Home != nil {
		home = *spec.Home
	}

	userPath := "/Users/" + spec.Username
	if _, err := m.run(ctx, true, "dscl", ".", "-create", userPath); err != nil {
		return err
	}
	if _, err := m.run(ctx, true, "dscl", ".", "-create", userPath, "UniqueID", strconv.Itoa(uid)); err != nil {
		return err
	}
	if _, err := m.run(ctx, true, "dscl", ".", "-create", userPath, "PrimaryGroupID", "20"); err != nil {
		return err
	}
	if _, err := m.run(ctx, true, "dscl", ".", "-create", userPath, "NFSHomeDirectory", home); err != nil {
		return err
	}
	if spec.Shell != nil {
		if _, err := m.run(ctx, true, "dscl", ".", "-create", userPath, "UserShell", *spec.Shell); err != nil {
			return err
		}
	}
	if spec.FullName != nil {
		if _, err := m.run(ctx, true, "dscl", ".", "-create", userPath, "RealName", *spec.FullName); err != nil {
			return err
		}
	}
	if spec.CreateHome {
		if _, err := m.run(ctx, true, "createhomedir", "-c", "-u", spec.Username); err != nil {
			return err
		}
	}

	return nil
}

func (m *CLIIdentityManager) updateDarwinUser(ctx context.Context, spec HostUserSpec) error {
	userPath := "/Users/" + spec.Username
	if spec.Home != nil {
		if _, err := m.run(ctx, true, "dscl", ".", "-create", userPath, "NFSHomeDirectory", *spec.Home); err != nil {
			return err
		}
	}
	if spec.Shell != nil {
		if _, err := m.run(ctx, true, "dscl", ".", "-create", userPath, "UserShell", *spec.Shell); err != nil {
			return err
		}
	}
	if spec.FullName != nil {
		if _, err := m.run(ctx, true, "dscl", ".", "-create", userPath, "RealName", *spec.FullName); err != nil {
			return err
		}
	}

	return nil
}

func (m *CLIIdentityManager) syncUserGroups(ctx context.Context, username string, desiredGroups []string, previousGroups []string) error {
	desired := stringSet(desiredGroups)
	previous := stringSet(previousGroups)

	status, exists, err := m.UserStatus(ctx, username)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("user %q does not exist", username)
	}
	current := stringSet(status.Groups)

	for _, group := range sortedSetDifference(desired, previous) {
		if _, ok := current[group]; ok {
			continue
		}
		if err := m.addUserToGroup(ctx, username, group); err != nil {
			return err
		}
	}

	for _, group := range sortedSetDifference(previous, desired) {
		if _, ok := current[group]; !ok {
			continue
		}
		if err := m.removeUserFromGroup(ctx, username, group); err != nil {
			return err
		}
	}

	return nil
}

func (m *CLIIdentityManager) addUserToGroup(ctx context.Context, username string, group string) error {
	switch m.goos {
	case "darwin":
		_, err := m.run(ctx, true, "dseditgroup", "-o", "edit", "-a", username, "-t", "user", group)
		return err
	default:
		_, err := m.run(ctx, true, "usermod", "-aG", group, username)
		return err
	}
}

func (m *CLIIdentityManager) removeUserFromGroup(ctx context.Context, username string, group string) error {
	switch m.goos {
	case "darwin":
		_, err := m.run(ctx, true, "dseditgroup", "-o", "edit", "-d", username, "-t", "user", group)
		return err
	default:
		_, err := m.run(ctx, true, "gpasswd", "-d", username, group)
		return err
	}
}

func (m *CLIIdentityManager) userGroupNames(ctx context.Context, username string) ([]string, error) {
	out, err := m.run(ctx, false, "id", "-nG", username)
	if err != nil {
		return nil, err
	}

	return parseIDGroupNames(string(out)), nil
}

func (m *CLIIdentityManager) nextDarwinID(ctx context.Context, recordPath string, attribute string, start int) (int, error) {
	out, err := m.run(ctx, false, "dscl", ".", "-list", recordPath, attribute)
	if err != nil {
		return 0, err
	}

	used := parseDSCLListIDs(string(out))
	for id := start; id < 60000; id++ {
		if !used[id] {
			return id, nil
		}
	}

	return 0, fmt.Errorf("could not find an available %s at or above %d", attribute, start)
}

func (m *CLIIdentityManager) runOptional(ctx context.Context, name string, args ...string) ([]byte, bool, error) {
	out, err := m.run(ctx, false, name, args...)
	if err == nil {
		return out, true, nil
	}

	if isIdentityNotFoundError(err) {
		return nil, false, nil
	}

	return nil, false, err
}

func (m *CLIIdentityManager) run(ctx context.Context, mutate bool, name string, args ...string) ([]byte, error) {
	commandName := name
	commandArgs := args

	if mutate && os.Geteuid() != 0 {
		if m.sudoPath == "" {
			return nil, fmt.Errorf("mutating identity commands require root privileges, but sudo was not found in PATH")
		}
		if err := m.authenticateSudo(ctx, name, args...); err != nil {
			return nil, err
		}

		commandName = m.sudoPath
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

func (m *CLIIdentityManager) authenticateSudo(ctx context.Context, name string, args ...string) error {
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
		return fmt.Errorf("sudo authentication failed: %w. Run `sudo -v` before `terraform apply`, or configure passwordless sudo for local identity management", err)
	}

	return nil
}

func parseGetentGroup(line string) (HostGroupStatus, error) {
	parts := strings.Split(line, ":")
	if len(parts) < 3 {
		return HostGroupStatus{}, fmt.Errorf("unexpected getent group output: %q", line)
	}

	return HostGroupStatus{
		Name: parts[0],
		GID:  parts[2],
	}, nil
}

func parseGetentPasswd(line string) (HostUserStatus, error) {
	parts := strings.Split(line, ":")
	if len(parts) < 7 {
		return HostUserStatus{}, fmt.Errorf("unexpected getent passwd output: %q", line)
	}

	fullName := strings.SplitN(parts[4], ",", 2)[0]

	return HostUserStatus{
		Username: parts[0],
		UID:      parts[2],
		GID:      parts[3],
		FullName: fullName,
		Home:     parts[5],
		Shell:    parts[6],
	}, nil
}

func parseIDGroupNames(out string) []string {
	fields := strings.Fields(out)
	sort.Strings(fields)
	return fields
}

func parseDSCLScalarAttribute(out string, key string) (string, error) {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", fmt.Errorf("missing dscl attribute %q", key)
	}

	prefix := key + ":"
	if strings.HasPrefix(lines[0], prefix) {
		value := strings.TrimSpace(strings.TrimPrefix(lines[0], prefix))
		if value != "" {
			return value, nil
		}

		var continued []string
		for _, line := range lines[1:] {
			line = strings.TrimSpace(line)
			if line != "" {
				continued = append(continued, line)
			}
		}
		return strings.Join(continued, "\n"), nil
	}

	return "", fmt.Errorf("unexpected dscl output for %q: %q", key, out)
}

func parseDSCLListIDs(out string) map[int]bool {
	used := map[int]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		id, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil {
			continue
		}
		used[id] = true
	}
	return used
}

func validateHostUserSpec(spec HostUserSpec) error {
	if err := validateHostUserName(spec.Username); err != nil {
		return err
	}
	if spec.Home != nil && !strings.HasPrefix(*spec.Home, "/") {
		return fmt.Errorf("home_dir must be an absolute path")
	}
	if spec.Shell != nil && !strings.HasPrefix(*spec.Shell, "/") {
		return fmt.Errorf("shell must be an absolute path")
	}
	for _, group := range spec.Groups {
		if err := validateHostGroupName(group); err != nil {
			return err
		}
	}
	return nil
}

func validateHostGroupName(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("group name must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(name, " \t\r\n:/") || strings.HasPrefix(name, "-") {
		return fmt.Errorf("group name %q is invalid; it must not contain whitespace, ':' or '/' and must not start with '-'", name)
	}
	return nil
}

func isIdentityNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(err.Error())
	return strings.Contains(text, "exit status 2") ||
		strings.Contains(text, "exit status 56") ||
		strings.Contains(text, "exit status 67") ||
		strings.Contains(text, "edsrecordnotfound") ||
		strings.Contains(text, "no such key") ||
		strings.Contains(text, "unknown user") ||
		strings.Contains(text, "unknown group")
}

func stringSet(values []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func sortedSetDifference(left map[string]struct{}, right map[string]struct{}) []string {
	var values []string
	for value := range left {
		if _, ok := right[value]; !ok {
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return values
}
