package provider

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

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
	got, err := gitResolveRemoteRef(context.Background(), gitPath, source, "main")
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
	state, err := resource.syncRepository(context.Background(), HostGitRepositoryResourceModel{
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
	state, err := resource.importRepositoryState(context.Background(), destination)
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

	out, err := runGit(context.Background(), gitPath, workDir, args...)
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
