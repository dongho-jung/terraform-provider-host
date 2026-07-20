package provider

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestHostGitRepositoryConfigureAllowsMissingGit(t *testing.T) {
	t.Parallel()

	resource := &HostGitRepositoryResource{}
	var resp frameworkresource.ConfigureResponse
	resource.Configure(t.Context(), frameworkresource.ConfigureRequest{
		ProviderData: HostProviderData{HomeDir: t.TempDir()},
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics)
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
	resource := &HostGitRepositoryResource{gitPath: gitPath}
	state, err := resource.syncRepository(t.Context(), HostGitRepositoryResourceModel{
		URL:             types.StringValue(source),
		Path:            types.StringValue(destination),
		Ref:             types.StringValue("main"),
		RemoteName:      types.StringValue("origin"),
		TrackRemote:     types.BoolValue(true),
		Recursive:       types.BoolValue(false),
		Force:           types.BoolValue(false),
		DeleteOnDestroy: types.BoolValue(true),
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
