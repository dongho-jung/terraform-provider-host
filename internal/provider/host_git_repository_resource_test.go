package provider

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestHostGitRepositoryConfigureAllowsMissingGit(t *testing.T) {
	t.Parallel()

	resource := &HostGitRepositoryResource{}
	var resp frameworkresource.ConfigureResponse
	resource.Configure(t.Context(), frameworkresource.ConfigureRequest{
		ProviderData: HostProviderData{TargetUser: "test", HomeDir: t.TempDir()},
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics)
	}
}

func TestHostGitRepositoryURLChangesDoNotRequireReplacement(t *testing.T) {
	t.Parallel()

	resource := &HostGitRepositoryResource{}
	var resp frameworkresource.SchemaResponse
	resource.Schema(t.Context(), frameworkresource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	urlAttribute, ok := resp.Schema.Attributes["url"].(resourceschema.StringAttribute)
	if !ok {
		t.Fatalf("url attribute has type %T, want schema.StringAttribute", resp.Schema.Attributes["url"])
	}
	requiresReplaceType := reflect.TypeOf(stringplanmodifier.RequiresReplace())
	for _, modifier := range urlAttribute.PlanModifiers {
		if reflect.TypeOf(modifier) == requiresReplaceType {
			t.Fatal("url changes must update the existing checkout instead of requiring replacement")
		}
	}
}

func TestHostGitRepositoryModifyPlanDefersMissingGit(t *testing.T) {
	t.Parallel()

	resolver := newLazyExecutablePath("git", "")
	resolver.lookPath = func(string) (string, error) {
		return "", errors.New("git not installed yet")
	}
	resource := &HostGitRepositoryResource{gitExecutable: resolver}
	ctx := t.Context()

	var schemaResp frameworkresource.SchemaResponse
	resource.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}
	model := HostGitRepositoryResourceModel{
		ID:              types.StringUnknown(),
		URL:             types.StringValue("https://example.com/repository.git"),
		Path:            types.StringValue(filepath.Join(t.TempDir(), "checkout")),
		PathResolved:    types.StringUnknown(),
		Ref:             types.StringValue("main"),
		RemoteName:      types.StringValue("origin"),
		TrackRemote:     types.BoolValue(true),
		Recursive:       types.BoolValue(false),
		Force:           types.BoolValue(false),
		DeleteOnDestroy: types.BoolValue(true),
		Commit:          types.StringUnknown(),
		RemoteCommit:    types.StringUnknown(),
		Dirty:           types.BoolUnknown(),
	}
	plan := tfsdk.Plan{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatalf("encode plan: %v", diags)
	}
	state := tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	resp := frameworkresource.ModifyPlanResponse{Plan: plan}
	resource.ModifyPlan(ctx, frameworkresource.ModifyPlanRequest{State: state, Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("modify plan diagnostics: %v", resp.Diagnostics)
	}

	var got HostGitRepositoryResourceModel
	if diags := resp.Plan.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode plan: %v", diags)
	}
	if !got.Commit.IsUnknown() || !got.RemoteCommit.IsUnknown() {
		t.Fatalf("remote values got commit=%#v remote_commit=%#v, want unknown", got.Commit, got.RemoteCommit)
	}
}

func TestHostGitRepositoryModifyPlanReusesRefreshedRemoteCommit(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	resource := &HostGitRepositoryResource{gitPath: filepath.Join(t.TempDir(), "must-not-run")}
	var schemaResp frameworkresource.SchemaResponse
	resource.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}

	path := filepath.Join(t.TempDir(), "checkout")
	const remoteCommit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	stateModel := HostGitRepositoryResourceModel{
		ID:              types.StringValue(path),
		URL:             types.StringValue("https://example.com/repository.git"),
		Path:            types.StringValue(path),
		PathResolved:    types.StringValue(path),
		Ref:             types.StringValue("main"),
		RemoteName:      types.StringValue("origin"),
		TrackRemote:     types.BoolValue(true),
		Recursive:       types.BoolValue(false),
		Force:           types.BoolValue(false),
		DeleteOnDestroy: types.BoolValue(true),
		Commit:          types.StringValue("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		RemoteCommit:    types.StringValue(remoteCommit),
		Dirty:           types.BoolValue(false),
	}
	state := tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	if diags := state.Set(ctx, &stateModel); diags.HasError() {
		t.Fatalf("encode state: %v", diags)
	}

	planModel := stateModel
	planModel.Commit = types.StringUnknown()
	planModel.RemoteCommit = types.StringUnknown()
	plan := tfsdk.Plan{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	if diags := plan.Set(ctx, &planModel); diags.HasError() {
		t.Fatalf("encode plan: %v", diags)
	}

	resp := frameworkresource.ModifyPlanResponse{Plan: plan}
	resource.ModifyPlan(ctx, frameworkresource.ModifyPlanRequest{State: state, Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("modify plan unexpectedly tried the remote: %v", resp.Diagnostics)
	}
	var got HostGitRepositoryResourceModel
	if diags := resp.Plan.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode plan: %v", diags)
	}
	if got.RemoteCommit.ValueString() != remoteCommit || got.Commit.ValueString() != remoteCommit {
		t.Fatalf("planned commits got commit=%#v remote=%#v", got.Commit, got.RemoteCommit)
	}
}

func TestSelectGitRemoteRefCommitPrefersBranch(t *testing.T) {
	t.Parallel()

	out := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\trefs/tags/main\nbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\trefs/heads/main\n"
	got, ok := selectGitRemoteRefCommit(out, "main")
	if !ok {
		t.Fatal("expected commit")
	}
	if got != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("got %q", got)
	}
}

func TestSelectGitRemoteRefCommitRejectsInvalidObjectID(t *testing.T) {
	t.Parallel()

	if commit, ok := selectGitRemoteRefCommit("--upload-pack=malicious\trefs/heads/main\n", "main"); ok {
		t.Fatalf("accepted invalid remote object ID %q", commit)
	}
}

func TestGitResolveRemoteRefWithLocalRepository(t *testing.T) {
	t.Parallel()

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}

	source := t.TempDir()
	runTestGit(t, gitPath, source, "init", "-b", "main")
	runTestGit(t, gitPath, source, "config", "user.email", "test@example.com")
	runTestGit(t, gitPath, source, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write README: %s", err)
	}
	runTestGit(t, gitPath, source, "add", "README.md")
	runTestGit(t, gitPath, source, "commit", "-m", "initial")

	wantBytes := runTestGit(t, gitPath, source, "rev-parse", "HEAD")
	want := stringTrimSpace(wantBytes)
	got, err := gitResolveRemoteRef(t.Context(), gitPath, source, "main")
	if err != nil {
		t.Fatalf("gitResolveRemoteRef: %s", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHostGitRepositorySyncClonesTrackedRef(t *testing.T) {
	t.Parallel()

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}

	source := t.TempDir()
	runTestGit(t, gitPath, source, "init", "-b", "main")
	runTestGit(t, gitPath, source, "config", "user.email", "test@example.com")
	runTestGit(t, gitPath, source, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write README: %s", err)
	}
	runTestGit(t, gitPath, source, "add", "README.md")
	runTestGit(t, gitPath, source, "commit", "-m", "initial")
	want := stringTrimSpace(runTestGit(t, gitPath, source, "rev-parse", "HEAD"))

	destination := filepath.Join(t.TempDir(), "checkout")
	recordingGitPath := writeRecordingGit(t, gitPath)
	resource := &HostGitRepositoryResource{gitPath: recordingGitPath}
	state, err := resource.syncRepository(t.Context(), HostGitRepositoryResourceModel{
		URL:             types.StringValue(source),
		Path:            types.StringValue(destination),
		Ref:             types.StringValue("main"),
		RemoteName:      types.StringValue("origin"),
		TrackRemote:     types.BoolValue(true),
		Recursive:       types.BoolValue(false),
		Force:           types.BoolValue(false),
		DeleteOnDestroy: types.BoolValue(true),
		RemoteCommit:    types.StringValue(want),
	})
	if err != nil {
		t.Fatalf("syncRepository: %s", err)
	}
	if state.Commit.ValueString() != want {
		t.Fatalf("commit got %q, want %q", state.Commit.ValueString(), want)
	}
	if _, err := os.Lstat(filepath.Join(destination, ".git")); err != nil {
		t.Fatalf("expected clone: %s", err)
	}
	log, err := os.ReadFile(recordingGitPath + ".log")
	if err != nil {
		t.Fatalf("read recording Git log: %s", err)
	}
	remoteQueries := 0
	for _, line := range splitNonEmptyLines(string(log)) {
		if strings.HasPrefix(line, "ls-remote ") {
			remoteQueries++
		}
	}
	if remoteQueries != 0 {
		t.Fatalf("ls-remote calls got %d, want 0 for a planned remote commit: %s", remoteQueries, log)
	}
}

func TestHostGitRepositoryUpdateChangesRemoteURLInPlace(t *testing.T) {
	t.Parallel()

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}

	sourceBefore := initTestGitRepository(t, gitPath, "before\n")
	sourceAfter := initTestGitRepository(t, gitPath, "after\n")
	destination := filepath.Join(t.TempDir(), "checkout")
	resource := &HostGitRepositoryResource{gitPath: gitPath}

	priorState, err := resource.syncRepository(t.Context(), HostGitRepositoryResourceModel{
		URL:             types.StringValue(sourceBefore),
		Path:            types.StringValue(destination),
		Ref:             types.StringNull(),
		RemoteName:      types.StringValue("origin"),
		TrackRemote:     types.BoolValue(false),
		Recursive:       types.BoolValue(false),
		Force:           types.BoolValue(false),
		DeleteOnDestroy: types.BoolValue(false),
	})
	if err != nil {
		t.Fatalf("initial sync: %s", err)
	}
	localFile := filepath.Join(destination, "local-only.txt")
	if err := os.WriteFile(localFile, []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("write local file: %s", err)
	}

	plan := priorState
	plan.URL = types.StringValue(sourceAfter)
	state, err := resource.syncRepositoryUpdate(t.Context(), priorState, plan)
	if err != nil {
		t.Fatalf("update remote URL: %s", err)
	}

	gotURL := stringTrimSpace(runTestGit(t, gitPath, destination, "remote", "get-url", "origin"))
	if gotURL != sourceAfter {
		t.Fatalf("remote URL got %q, want %q", gotURL, sourceAfter)
	}
	if _, err := os.Stat(localFile); err != nil {
		t.Fatalf("in-place update removed local file: %s", err)
	}
	if !state.Dirty.ValueBool() {
		t.Fatal("expected preserved local file to keep checkout dirty")
	}
	if state.URL.ValueString() != sourceAfter {
		t.Fatalf("state URL got %q, want %q", state.URL.ValueString(), sourceAfter)
	}
}

func TestHostGitRepositoryUpdateRefusesUnexpectedRemoteDrift(t *testing.T) {
	t.Parallel()

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}

	sourceInState := initTestGitRepository(t, gitPath, "state\n")
	sourceFromDrift := initTestGitRepository(t, gitPath, "drift\n")
	sourcePlanned := initTestGitRepository(t, gitPath, "planned\n")
	destination := filepath.Join(t.TempDir(), "checkout")
	resource := &HostGitRepositoryResource{gitPath: gitPath}

	priorState, err := resource.syncRepository(t.Context(), HostGitRepositoryResourceModel{
		URL:             types.StringValue(sourceInState),
		Path:            types.StringValue(destination),
		Ref:             types.StringNull(),
		RemoteName:      types.StringValue("origin"),
		TrackRemote:     types.BoolValue(false),
		Recursive:       types.BoolValue(false),
		Force:           types.BoolValue(false),
		DeleteOnDestroy: types.BoolValue(false),
	})
	if err != nil {
		t.Fatalf("initial sync: %s", err)
	}
	runTestGit(t, gitPath, destination, "remote", "set-url", "origin", sourceFromDrift)

	plan := priorState
	plan.URL = types.StringValue(sourcePlanned)
	if _, err := resource.syncRepositoryUpdate(t.Context(), priorState, plan); err == nil {
		t.Fatal("update unexpectedly replaced a remotely drifted URL")
	} else if !strings.Contains(err.Error(), "changed outside Terraform") {
		t.Fatalf("unexpected drift error: %s", err)
	}

	gotURL := stringTrimSpace(runTestGit(t, gitPath, destination, "remote", "get-url", "origin"))
	if gotURL != sourceFromDrift {
		t.Fatalf("failed update changed remote URL to %q, want drifted value %q preserved", gotURL, sourceFromDrift)
	}
}

func TestHostGitRepositorySyncRetriesGitInstalledAfterConfigure(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}

	source := t.TempDir()
	runTestGit(t, gitPath, source, "init", "-b", "main")
	runTestGit(t, gitPath, source, "config", "user.email", "test@example.com")
	runTestGit(t, gitPath, source, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write README: %s", err)
	}
	runTestGit(t, gitPath, source, "add", "README.md")
	runTestGit(t, gitPath, source, "commit", "-m", "initial")

	resolver := newLazyExecutablePath("git", "")
	var lookups atomic.Int32
	resolver.lookPath = func(string) (string, error) {
		if lookups.Add(1) == 1 {
			return "", errors.New("git not installed yet")
		}
		return gitPath, nil
	}
	resource := &HostGitRepositoryResource{gitExecutable: resolver}
	destination := filepath.Join(t.TempDir(), "checkout")
	model := HostGitRepositoryResourceModel{
		URL:             types.StringValue(source),
		Path:            types.StringValue(destination),
		Ref:             types.StringValue("main"),
		RemoteName:      types.StringValue("origin"),
		TrackRemote:     types.BoolValue(false),
		Recursive:       types.BoolValue(false),
		Force:           types.BoolValue(false),
		DeleteOnDestroy: types.BoolValue(true),
	}

	if _, err := resource.syncRepository(t.Context(), model); err == nil {
		t.Fatal("sync before git installation unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "Git repository sync") || !strings.Contains(err.Error(), `executable "git"`) {
		t.Fatalf("unexpected operation-time error: %s", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed lookup created destination: %v", err)
	}

	state, err := resource.syncRepository(t.Context(), model)
	if err != nil {
		t.Fatalf("sync after git installation: %s", err)
	}
	if state.Commit.IsNull() || state.Commit.IsUnknown() {
		t.Fatalf("commit was not populated: %#v", state.Commit)
	}
	if lookups.Load() != 2 {
		t.Fatalf("lookups got %d, want 2", lookups.Load())
	}
}

func TestHostGitRepositoryImportStateReadsExistingCheckout(t *testing.T) {
	t.Parallel()

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}

	source := t.TempDir()
	runTestGit(t, gitPath, source, "init", "-b", "main")
	runTestGit(t, gitPath, source, "config", "user.email", "test@example.com")
	runTestGit(t, gitPath, source, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write README: %s", err)
	}
	runTestGit(t, gitPath, source, "add", "README.md")
	runTestGit(t, gitPath, source, "commit", "-m", "initial")
	wantCommit := stringTrimSpace(runTestGit(t, gitPath, source, "rev-parse", "HEAD"))

	destination := filepath.Join(t.TempDir(), "checkout")
	runTestGit(t, gitPath, "", "clone", source, destination)

	resource := &HostGitRepositoryResource{gitPath: gitPath}
	state, err := resource.importRepositoryState(t.Context(), destination)
	if err != nil {
		t.Fatalf("importRepositoryState: %s", err)
	}
	if state.URL.ValueString() != source {
		t.Fatalf("url got %q, want %q", state.URL.ValueString(), source)
	}
	if state.Commit.ValueString() != wantCommit {
		t.Fatalf("commit got %q, want %q", state.Commit.ValueString(), wantCommit)
	}
	if state.DeleteOnDestroy.ValueBool() {
		t.Fatal("delete_on_destroy should default to false on import")
	}
}

func TestHostGitRepositoryDeleteRefusesHomeDirectory(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	resource := &HostGitRepositoryResource{
		homeDir:    homeDir,
		runtimeDir: filepath.Join(homeDir, ".local", "state", providerRuntimeDirName),
	}
	err := resource.deleteRepository(t.Context(), HostGitRepositoryResourceModel{
		URL:             types.StringValue("https://example.com/repository.git"),
		Path:            types.StringValue(homeDir),
		Ref:             types.StringNull(),
		RemoteName:      types.StringValue("origin"),
		TrackRemote:     types.BoolValue(false),
		Recursive:       types.BoolValue(false),
		Force:           types.BoolValue(false),
		DeleteOnDestroy: types.BoolValue(true),
	})
	if err == nil {
		t.Fatal("expected protected home directory error")
	}
}

func initTestGitRepository(t *testing.T, gitPath string, contents string) string {
	t.Helper()

	source := t.TempDir()
	runTestGit(t, gitPath, source, "init", "-b", "main")
	runTestGit(t, gitPath, source, "config", "user.email", "test@example.com")
	runTestGit(t, gitPath, source, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write README: %s", err)
	}
	runTestGit(t, gitPath, source, "add", "README.md")
	runTestGit(t, gitPath, source, "commit", "-m", "initial")
	return source
}

func runTestGit(t *testing.T, gitPath string, workDir string, args ...string) []byte {
	t.Helper()

	out, err := runGit(t.Context(), gitPath, workDir, args...)
	if err != nil {
		t.Fatalf("git %v: %s", args, err)
	}
	return out
}

func stringTrimSpace(value []byte) string {
	return string(bytesTrimSpace(value))
}

func bytesTrimSpace(value []byte) []byte {
	for len(value) > 0 && (value[0] == ' ' || value[0] == '\n' || value[0] == '\r' || value[0] == '\t') {
		value = value[1:]
	}
	for len(value) > 0 {
		last := value[len(value)-1]
		if last != ' ' && last != '\n' && last != '\r' && last != '\t' {
			break
		}
		value = value[:len(value)-1]
	}
	return value
}

func writeRecordingGit(t *testing.T, gitPath string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"${0}.log\"\n" +
		"exec " + shellQuote(gitPath) + " \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write recording Git: %s", err)
	}
	return path
}
