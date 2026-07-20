package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type directHostSystemFileCommandRunner struct{}

func (directHostSystemFileCommandRunner) Run(ctx context.Context, stdin io.Reader, name string, args ...string) (hostSystemFileCommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := hostSystemFileCommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, err
}

func (directHostSystemFileCommandRunner) NeedsPrivilegeEscalation() bool { return false }

type recordingDirectHostSystemFileCommandRunner struct {
	calls []recordedHostSystemFileCommand
}

type recordedHostSystemFileCommand struct {
	name  string
	args  []string
	stdin []byte
}

func (r *recordingDirectHostSystemFileCommandRunner) Run(ctx context.Context, stdin io.Reader, name string, args ...string) (hostSystemFileCommandResult, error) {
	var input []byte
	var err error
	if stdin != nil {
		input, err = io.ReadAll(stdin)
		if err != nil {
			return hostSystemFileCommandResult{}, err
		}
		stdin = bytes.NewReader(input)
	}
	r.calls = append(r.calls, recordedHostSystemFileCommand{name: name, args: append([]string(nil), args...), stdin: input})
	return (directHostSystemFileCommandRunner{}).Run(ctx, stdin, name, args...)
}

func (r *recordingDirectHostSystemFileCommandRunner) NeedsPrivilegeEscalation() bool { return false }

type scriptedHostSystemFileCommandRunner struct {
	results []hostSystemFileCommandResult
	calls   []string
}

func (r *scriptedHostSystemFileCommandRunner) Run(_ context.Context, _ io.Reader, name string, args ...string) (hostSystemFileCommandResult, error) {
	r.calls = append(r.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	if len(r.results) == 0 {
		return hostSystemFileCommandResult{}, errors.New("unexpected command")
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result, nil
}

func (r *scriptedHostSystemFileCommandRunner) NeedsPrivilegeEscalation() bool { return true }

type recordingHostSystemFileManager struct {
	fileCalls    int
	installCalls []HostSystemFileSpec
	status       HostSystemFileStatus
	exists       bool
}

func (m *recordingHostSystemFileManager) File(context.Context, string) (HostSystemFileStatus, bool, error) {
	m.fileCalls++
	return m.status, m.exists, nil
}

func (m *recordingHostSystemFileManager) InstallFile(_ context.Context, spec HostSystemFileSpec) (HostSystemFileStatus, error) {
	m.installCalls = append(m.installCalls, spec)
	return m.status, nil
}

func (m *recordingHostSystemFileManager) DeleteFile(context.Context, string, string) error {
	return nil
}

func (m *recordingHostSystemFileManager) NeedsPrivilegeEscalation() bool { return false }

func TestCLIHostSystemFileManagerInstallAndSafeDelete(t *testing.T) {
	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("current user: %s", err)
	}
	currentGroup, err := user.LookupGroupId(currentUser.Gid)
	if err != nil {
		t.Fatalf("current group: %s", err)
	}
	for _, command := range []string{"dd", "install", "mv", "mkdir", "rm"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is not available", command)
		}
	}

	runner := &recordingDirectHostSystemFileCommandRunner{}
	manager := newCLIHostSystemFileManagerWithRunner(runner, "linux")
	destinationRoot := t.TempDir()
	// A privileged install must not create or reopen anything in TMPDIR.
	t.Setenv("TMPDIR", filepath.Join(destinationRoot, "missing-untrusted-tmp"))
	destination := filepath.Join(destinationRoot, "nested", "vpn-up")
	spec := HostSystemFileSpec{
		Destination: destination,
		Content:     []byte("#!/bin/sh\necho connected\n"),
		Mode:        0o750,
		Owner:       currentUser.Username,
		Group:       currentGroup.Name,
	}

	status, err := manager.InstallFile(t.Context(), spec)
	if err != nil {
		t.Fatalf("install file: %s", err)
	}
	if !hostSystemFileMatchesSpec(status, spec) {
		t.Fatalf("installed status does not match spec: %#v", status)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read installed file: %s", err)
	}
	if !bytes.Equal(content, spec.Content) {
		t.Fatalf("content got %q, want %q", content, spec.Content)
	}
	var ddCalls []recordedHostSystemFileCommand
	for _, call := range runner.calls {
		if call.name == "dd" {
			ddCalls = append(ddCalls, call)
		}
		for _, arg := range call.args {
			if strings.Contains(arg, os.Getenv("TMPDIR")) {
				t.Fatalf("privileged pipeline referenced TMPDIR in %s %#v", call.name, call.args)
			}
		}
	}
	if len(ddCalls) != 1 || !bytes.Equal(ddCalls[0].stdin, spec.Content) {
		t.Fatalf("dd stdin calls got %#v, want one call with %q", ddCalls, spec.Content)
	}

	if err := os.WriteFile(destination, []byte("external drift\n"), 0o750); err != nil {
		t.Fatalf("write drift: %s", err)
	}
	if err := manager.DeleteFile(t.Context(), destination, status.ChecksumSHA256); err == nil || !strings.Contains(err.Error(), "refusing to delete") {
		t.Fatalf("delete drifted file error got %v", err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("drifted file should remain: %s", err)
	}

	driftStatus, exists, err := manager.File(t.Context(), destination)
	if err != nil || !exists {
		t.Fatalf("read drift status: exists=%t err=%v", exists, err)
	}
	if err := manager.DeleteFile(t.Context(), destination, driftStatus.ChecksumSHA256); err != nil {
		t.Fatalf("delete matching file: %s", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("file still exists or unexpected stat error: %v", err)
	}
}

func TestReadHostSystemFileRejectsSymlinkAndDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatalf("write target: %s", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %s", err)
	}
	if _, _, err := readHostSystemFileDirect(link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink error got %v", err)
	}
	if _, _, err := readHostSystemFileDirect(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory error got %v", err)
	}
}

func TestCLIHostSystemFileManagerPrivilegedRead(t *testing.T) {
	runner := &scriptedHostSystemFileCommandRunner{results: []hostSystemFileCommandResult{
		{ExitCode: 1},
		{ExitCode: 0},
		{ExitCode: 0},
		{Stdout: []byte("440\t0\t0\n")},
		{Stdout: []byte("dongho ALL=(root) NOPASSWD: /usr/bin/true\n")},
	}}
	manager := newCLIHostSystemFileManagerWithRunner(runner, "linux")
	status, exists, err := manager.readFilePrivileged(t.Context(), "/etc/sudoers.d/test")
	if err != nil || !exists {
		t.Fatalf("privileged read: exists=%t err=%v", exists, err)
	}
	if status.Mode != 0o440 || status.Owner != "root" || status.Group != "root" {
		t.Fatalf("unexpected metadata: %#v", status)
	}
	wantCalls := []string{
		"test -L /etc/sudoers.d/test",
		"test -e /etc/sudoers.d/test",
		"test -f /etc/sudoers.d/test",
		"stat -c %a\t%u\t%g /etc/sudoers.d/test",
		"cat /etc/sudoers.d/test",
	}
	if strings.Join(runner.calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("calls got %#v, want %#v", runner.calls, wantCalls)
	}
}

func TestCLIHostSystemFileManagerFreeBSDPrivilegedReadUsesBSDStat(t *testing.T) {
	runner := &scriptedHostSystemFileCommandRunner{results: []hostSystemFileCommandResult{
		{ExitCode: 1},
		{ExitCode: 0},
		{ExitCode: 0},
		{Stdout: []byte("440\t0\t0\n")},
		{Stdout: []byte("root ALL=(root) /usr/bin/true\n")},
	}}
	manager := newCLIHostSystemFileManagerWithRunner(runner, "freebsd")
	_, exists, err := manager.readFilePrivileged(t.Context(), "/usr/local/etc/sudoers.d/test")
	if err != nil || !exists {
		t.Fatalf("FreeBSD privileged read: exists=%t err=%v", exists, err)
	}
	wantStatCall := "stat -f %Lp\t%u\t%g /usr/local/etc/sudoers.d/test"
	if len(runner.calls) < 4 || runner.calls[3] != wantStatCall {
		t.Fatalf("FreeBSD stat call got %#v, want %q", runner.calls, wantStatCall)
	}
}

func TestHostSystemFileContentFromModelUsesSourceWithoutPersistingBytes(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "vpn-up")
	content := []byte("#!/bin/sh\nexec openvpn\n")
	if err := os.WriteFile(source, content, 0o755); err != nil {
		t.Fatalf("write source: %s", err)
	}
	model := HostSystemFileResourceModel{
		Source:  types.StringValue(source),
		Content: types.StringNull(),
	}
	got, resolved, err := hostSystemFileContentFromModel(model, "")
	if err != nil {
		t.Fatalf("source content: %s", err)
	}
	if !bytes.Equal(got, content) || resolved != source {
		t.Fatalf("got content=%q resolved=%q", got, resolved)
	}
	if !model.Content.IsNull() {
		t.Fatal("source-backed model unexpectedly acquired content state")
	}
}

func TestHostSystemFileContentFromModelRequiresExactlyOneInput(t *testing.T) {
	models := []HostSystemFileResourceModel{
		{Source: types.StringNull(), Content: types.StringNull()},
		{Source: types.StringValue("source"), Content: types.StringValue("content")},
	}
	for _, model := range models {
		if _, _, err := hostSystemFileContentFromModel(model, ""); err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("expected exactly-one error, got %v", err)
		}
	}
}

func TestHostSystemFileApplyRejectsSourceChangedAfterPlanBeforeManagerCalls(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "vpn-up")
	plannedContent := []byte("#!/bin/sh\necho planned\n")
	changedContent := []byte("#!/bin/sh\necho changed\n")
	if err := os.WriteFile(source, plannedContent, 0o755); err != nil {
		t.Fatalf("write planned source: %s", err)
	}
	plannedChecksum := hostSystemFileChecksum(plannedContent)
	if err := os.WriteFile(source, changedContent, 0o755); err != nil {
		t.Fatalf("change source after plan: %s", err)
	}

	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			manager := &recordingHostSystemFileManager{}
			r := &HostSystemFileResource{manager: manager}
			var schemaResp frameworkresource.SchemaResponse
			r.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
			if schemaResp.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
			}
			model := HostSystemFileResourceModel{
				ID:                     types.StringValue("/usr/local/bin/vpn-up"),
				Destination:            types.StringValue("/usr/local/bin/vpn-up"),
				Source:                 types.StringValue(source),
				Content:                types.StringNull(),
				SourcePath:             types.StringValue(source),
				ChecksumSHA256:         types.StringValue(plannedChecksum),
				DeployedChecksumSHA256: types.StringUnknown(),
				Mode:                   types.StringValue("0755"),
				Owner:                  types.StringValue("root"),
				Group:                  types.StringValue(hostSystemRootGroup()),
				AdoptExisting:          types.BoolValue(false),
				DeleteOnDestroy:        types.BoolValue(false),
			}
			plan := tfsdk.Plan{
				Schema: schemaResp.Schema,
				Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
			}
			if diags := plan.Set(ctx, &model); diags.HasError() {
				t.Fatalf("encode plan: %v", diags)
			}

			var hasError bool
			var detail string
			switch operation {
			case "create":
				var resp frameworkresource.CreateResponse
				r.Create(ctx, frameworkresource.CreateRequest{Plan: plan}, &resp)
				hasError = resp.Diagnostics.HasError()
				if hasError {
					detail = resp.Diagnostics.Errors()[0].Detail()
				}
			case "update":
				var resp frameworkresource.UpdateResponse
				r.Update(ctx, frameworkresource.UpdateRequest{Plan: plan}, &resp)
				hasError = resp.Diagnostics.HasError()
				if hasError {
					detail = resp.Diagnostics.Errors()[0].Detail()
				}
			}
			if !hasError || !strings.Contains(detail, "changed after planning") {
				t.Fatalf("%s diagnostics got error=%t detail=%q", operation, hasError, detail)
			}
			if manager.fileCalls != 0 || len(manager.installCalls) != 0 {
				t.Fatalf("%s reached manager after source drift: file_calls=%d install_calls=%d", operation, manager.fileCalls, len(manager.installCalls))
			}
		})
	}
}

func TestValidateHostSystemFileDestination(t *testing.T) {
	for _, valid := range []string{"/usr/local/bin/vpn-up", "/etc/openvpn/client/profile.ovpn"} {
		if err := validateHostSystemFileDestination(valid); err != nil {
			t.Fatalf("valid destination %q: %s", valid, err)
		}
	}
	for _, invalid := range []string{"", "relative", "/", "/etc/../tmp/file", "/tmp/file/"} {
		if err := validateHostSystemFileDestination(invalid); err == nil {
			t.Fatalf("expected invalid destination %q", invalid)
		}
	}
}

func TestTrustedHostSystemExecutableIgnoresCallerPATH(t *testing.T) {
	maliciousDirectory := t.TempDir()
	maliciousInstall := filepath.Join(maliciousDirectory, "install")
	if err := os.WriteFile(maliciousInstall, []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
		t.Fatalf("write malicious utility: %s", err)
	}
	t.Setenv("PATH", maliciousDirectory)

	resolved, err := trustedHostSystemExecutable("install")
	if err != nil {
		t.Fatalf("resolve trusted install: %s", err)
	}
	if resolved == maliciousInstall || !filepath.IsAbs(resolved) {
		t.Fatalf("resolved untrusted utility %q", resolved)
	}
	canonical, err := filepath.EvalSymlinks(resolved)
	if err != nil || canonical != resolved {
		t.Fatalf("resolved utility is not canonical: resolved=%q canonical=%q err=%v", resolved, canonical, err)
	}
	if _, err := trustedHostSystemExecutable("sh"); err == nil {
		t.Fatal("unexpectedly resolved non-allowlisted utility")
	}
}

func TestValidateHostSystemFileProtectedParents(t *testing.T) {
	if err := validateHostSystemFileProtectedParents("/usr/local/bin/terraform-provider-host-test"); err != nil {
		t.Fatalf("trusted system destination: %s", err)
	}
	untrustedDestination := filepath.Join(t.TempDir(), "system-file")
	if err := validateHostSystemFileProtectedParents(untrustedDestination); err == nil || !strings.Contains(err.Error(), "root-owned") {
		t.Fatalf("untrusted destination error got %v", err)
	}
}

func TestValidateHostSystemFileInstallPlatform(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "freebsd"} {
		if err := validateHostSystemFileInstallPlatform(goos); err != nil {
			t.Errorf("supported platform %s: %s", goos, err)
		}
	}
	for _, goos := range []string{"windows", "plan9"} {
		if err := validateHostSystemFileInstallPlatform(goos); err == nil {
			t.Errorf("unsupported platform %s was accepted", goos)
		}
	}
}

func TestValidateHostSystemFileProtectedSpecAndDeleteStatus(t *testing.T) {
	spec := HostSystemFileSpec{
		Destination: "/usr/local/bin/vpn-up",
		Mode:        0o755,
		Owner:       "root",
		Group:       hostSystemRootGroup(),
	}
	if err := validateHostSystemFileProtectedSpec(spec); err != nil {
		t.Fatalf("valid protected spec: %s", err)
	}
	nonRoot := spec
	nonRoot.Owner = "nobody"
	if err := validateHostSystemFileProtectedSpec(nonRoot); err == nil || !strings.Contains(err.Error(), "must be root") {
		t.Fatalf("non-root owner error got %v", err)
	}
	writable := spec
	writable.Mode = 0o775
	if err := validateHostSystemFileProtectedSpec(writable); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("group-writable mode error got %v", err)
	}

	status := HostSystemFileStatus{Owner: "root", Group: hostSystemRootGroup(), Mode: 0o755}
	if err := validateHostSystemFileDeleteStatus(status); err != nil {
		t.Fatalf("safe delete status: %s", err)
	}
	status.Owner = "nobody"
	if err := validateHostSystemFileDeleteStatus(status); err == nil {
		t.Fatal("non-root delete status was accepted")
	}
	status.Owner = "root"
	status.Mode = 0o755 | os.ModeSetuid
	if err := validateHostSystemFileDeleteStatus(status); err == nil || !strings.Contains(err.Error(), "setuid") {
		t.Fatalf("special mode delete error got %v", err)
	}
}

func TestParseHostSystemFileStat(t *testing.T) {
	mode, owner, group, err := parseHostSystemFileStat("440\t0\t0\n")
	if err != nil {
		t.Fatalf("parse stat: %s", err)
	}
	if mode != 0o440 || owner != "root" || group != hostSystemRootGroup() {
		t.Fatalf("got mode=%04o owner=%q group=%q", mode, owner, group)
	}
	mode, _, _, err = parseHostSystemFileStat("4755\t0\t0\n")
	if err != nil || mode.Perm() != 0o755 || mode&os.ModeSetuid == 0 || formatHostSystemFileMode(mode) != "4755" {
		t.Fatalf("parse setuid mode got %s (%v), err=%v", formatHostSystemFileMode(mode), mode, err)
	}
	mode, _, _, err = parseHostSystemFileStat("0\t0\t0\n")
	if err != nil || mode != 0 {
		t.Fatalf("parse zero mode got %04o, err=%v", mode, err)
	}
}

func TestHostSystemFileSchemaValid(t *testing.T) {
	resource := NewHostSystemFileResource()
	var response frameworkresource.SchemaResponse
	resource.Schema(t.Context(), frameworkresource.SchemaRequest{}, &response)
	if diags := response.Schema.ValidateImplementation(t.Context()); diags.HasError() {
		t.Fatalf("invalid host_system_file schema: %v", diags)
	}
}

func TestProviderProtocolExposesPrivilegedFileSchemas(t *testing.T) {
	server := providerserver.NewProtocol6(New("test")())()
	response, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("get provider schema: %s", err)
	}
	for _, diagnostic := range response.Diagnostics {
		if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("provider schema diagnostic: %s: %s", diagnostic.Summary, diagnostic.Detail)
		}
	}
	for _, name := range []string{"host_system_file", "host_sudoers_rule"} {
		if response.ResourceSchemas[name] == nil {
			t.Fatalf("provider schema is missing %s", name)
		}
	}
}
