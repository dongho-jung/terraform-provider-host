package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestResolveHostLinkSourceRelativeToWorkingDirectory(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)

	resolvedWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd after chdir: %s", err)
	}

	got, err := resolveHostLinkSourceForHome("./nvim", t.TempDir())
	if err != nil {
		t.Fatalf("resolveHostLinkSourceForHome: %s", err)
	}
	want := filepath.Join(resolvedWorkingDir, "nvim")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWriteHostLinkCreatesSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %s", err)
	}

	link := filepath.Join(root, "config", "nvim")
	if err := writeHostLink(link, target); err != nil {
		t.Fatalf("writeHostLink: %s", err)
	}

	got, exists, err := readHostLinkSource(link)
	if err != nil {
		t.Fatalf("readHostLinkSource: %s", err)
	}
	if !exists {
		t.Fatal("expected link to exist")
	}
	if got != target {
		t.Fatalf("got %q, want %q", got, target)
	}
}

func TestWriteHostLinkAtomicallyReplacesSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	firstTarget := filepath.Join(root, "first")
	secondTarget := filepath.Join(root, "second")
	for _, target := range []string{firstTarget, secondTarget} {
		if err := os.WriteFile(target, []byte(filepath.Base(target)), 0o600); err != nil {
			t.Fatalf("write target %q: %s", target, err)
		}
	}

	link := filepath.Join(root, "current")
	if err := os.Symlink(firstTarget, link); err != nil {
		t.Fatalf("create initial link: %s", err)
	}
	if err := writeHostLink(link, secondTarget); err != nil {
		t.Fatalf("replace link: %s", err)
	}

	got, exists, err := readHostLinkSource(link)
	if err != nil {
		t.Fatalf("read replaced link: %s", err)
	}
	if !exists || got != secondTarget {
		t.Fatalf("replaced link = %q, exists=%t; want %q", got, exists, secondTarget)
	}
}

func TestWriteHostLinkRejectsExistingDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %s", err)
	}
	link := filepath.Join(root, "nvim")
	if err := os.Mkdir(link, 0o755); err != nil {
		t.Fatalf("mkdir link path: %s", err)
	}

	if err := writeHostLink(link, target); err == nil {
		t.Fatal("expected existing directory error")
	}
}

func TestEnsureHostLinkSourceExistsRejectsMissing(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing")
	if err := ensureHostLinkSourceExists(missing); err == nil {
		t.Fatal("expected missing source error")
	}
}

func TestDeleteHostLinkRefusesRegularDirectory(t *testing.T) {
	t.Parallel()

	link := filepath.Join(t.TempDir(), "nvim")
	if err := os.Mkdir(link, 0o755); err != nil {
		t.Fatalf("mkdir link path: %s", err)
	}

	if err := deleteHostLink(link); err == nil {
		t.Fatal("expected existing directory error")
	}
}

func TestHostLinkSourceDigestTracksContentAndMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o750); err != nil {
		t.Fatalf("create source: %s", err)
	}
	file := filepath.Join(source, "config")
	if err := os.WriteFile(file, []byte("first"), 0o600); err != nil {
		t.Fatalf("write source: %s", err)
	}

	first, err := hostLinkSourceDigest(source)
	if err != nil {
		t.Fatalf("digest source: %s", err)
	}
	if !isSHA256Hex(first) {
		t.Fatalf("digest %q is not lowercase SHA256", first)
	}

	if err := os.WriteFile(file, []byte("second"), 0o600); err != nil {
		t.Fatalf("change source content: %s", err)
	}
	second, err := hostLinkSourceDigest(source)
	if err != nil {
		t.Fatalf("digest changed content: %s", err)
	}
	if second == first {
		t.Fatal("content change did not change digest")
	}

	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatalf("change source mode: %s", err)
	}
	third, err := hostLinkSourceDigest(source)
	if err != nil {
		t.Fatalf("digest changed mode: %s", err)
	}
	if third == second {
		t.Fatal("mode change did not change digest")
	}
}

func TestHostLinkResourceStagesSourceOutsideWorkingTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "checkout", "config")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatalf("create source: %s", err)
	}
	if err := os.WriteFile(filepath.Join(source, "settings"), []byte("managed"), 0o640); err != nil {
		t.Fatalf("write source: %s", err)
	}

	homeDir := filepath.Join(root, "home")
	runtimeDir := filepath.Join(homeDir, ".local", "state", "terraform-provider-host")
	resource := &HostLinkResource{homeDir: homeDir, runtimeDir: runtimeDir}
	model, err := resource.syncLink(HostLinkResourceModel{
		Source:      types.StringValue(source),
		StageSource: types.BoolValue(true),
		Destination: types.StringValue("~/.config/example"),
	})
	if err != nil {
		t.Fatalf("sync staged link: %s", err)
	}

	stagedSource := model.SourcePath.ValueString()
	if !strings.HasPrefix(stagedSource, runtimeDir+string(os.PathSeparator)) {
		t.Fatalf("staged source %q is outside runtime %q", stagedSource, runtimeDir)
	}
	if got, err := os.ReadFile(filepath.Join(stagedSource, "settings")); err != nil || string(got) != "managed" {
		t.Fatalf("read staged content: got %q, err=%v", got, err)
	}
	if err := os.RemoveAll(filepath.Join(root, "checkout")); err != nil {
		t.Fatalf("remove checkout: %s", err)
	}

	linkSource, exists, err := readHostLinkSource(model.DestinationPath.ValueString())
	if err != nil {
		t.Fatalf("read managed link: %s", err)
	}
	if !exists || linkSource != stagedSource {
		t.Fatalf("managed link = %q, exists=%t; want %q", linkSource, exists, stagedSource)
	}
	if got, err := os.ReadFile(filepath.Join(linkSource, "settings")); err != nil || string(got) != "managed" {
		t.Fatalf("read staged content after checkout removal: got %q, err=%v", got, err)
	}
}

func TestHostLinkResourceKeepsStagedSourcePathAcrossContentChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "checkout", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(source), 0o750); err != nil {
		t.Fatalf("create source parent: %s", err)
	}
	if err := os.WriteFile(source, []byte("first"), 0o640); err != nil {
		t.Fatalf("write source: %s", err)
	}

	homeDir := filepath.Join(root, "home")
	resource := &HostLinkResource{
		homeDir:    homeDir,
		runtimeDir: filepath.Join(homeDir, ".local", "state", "terraform-provider-host"),
	}
	model := HostLinkResourceModel{
		Source:      types.StringValue(source),
		StageSource: types.BoolValue(true),
		Destination: types.StringValue("~/.claude/CLAUDE.md"),
	}

	before, err := resource.syncLink(model)
	if err != nil {
		t.Fatalf("sync staged link: %s", err)
	}
	if err := os.WriteFile(source, []byte("second"), 0o640); err != nil {
		t.Fatalf("rewrite source: %s", err)
	}
	after, err := resource.syncLink(model)
	if err != nil {
		t.Fatalf("resync staged link: %s", err)
	}

	// Only the digest tracks content, so a source edit does not drag a pair of
	// content-addressed paths through the plan diff.
	if after.SourcePath.ValueString() != before.SourcePath.ValueString() {
		t.Fatalf("staged source_path churned: %q -> %q", before.SourcePath.ValueString(), after.SourcePath.ValueString())
	}
	if after.SourceDigest.ValueString() == before.SourceDigest.ValueString() {
		t.Fatalf("source_digest %q did not track the content change", after.SourceDigest.ValueString())
	}
	if got, err := os.ReadFile(after.DestinationPath.ValueString()); err != nil || string(got) != "second" {
		t.Fatalf("managed link content got %q, err=%v", string(got), err)
	}

	// The refresh digests what the stable indirection resolves to, otherwise it
	// would hash the symbolic link itself and report drift on every read.
	linkSource, exists, err := readHostLinkSource(after.DestinationPath.ValueString())
	if err != nil || !exists {
		t.Fatalf("read managed link: exists=%t, err=%v", exists, err)
	}
	refreshed, err := hostLinkStagedSourceDigest(linkSource)
	if err != nil {
		t.Fatalf("digest staged source: %s", err)
	}
	if refreshed != after.SourceDigest.ValueString() {
		t.Fatalf("refreshed digest %q, want %q", refreshed, after.SourceDigest.ValueString())
	}
}

func TestRemoveHostLinkStageRootRejectsBroadPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := removeHostLinkStageRoot(root); err == nil {
		t.Fatal("expected unsafe stage root error")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("unsafe root was changed: %s", err)
	}
}

func TestHostLinkResourceImportStateHydratesSourceBeforeRead(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	homeDir := t.TempDir()
	target := filepath.Join(homeDir, "target")
	if err := os.WriteFile(target, []byte("content"), 0o600); err != nil {
		t.Fatalf("write target: %s", err)
	}
	link := filepath.Join(homeDir, ".config", "current")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("create link parent: %s", err)
	}
	if err := os.Symlink("../target", link); err != nil {
		t.Fatalf("create symbolic link: %s", err)
	}

	r := &HostLinkResource{homeDir: homeDir}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}

	importResp := resource.ImportStateResponse{
		State: tfsdk.State{
			Schema: schemaResp.Schema,
			Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
		},
	}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "~/.config/current"}, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", importResp.Diagnostics)
	}

	var imported HostLinkResourceModel
	if diags := importResp.State.Get(ctx, &imported); diags.HasError() {
		t.Fatalf("decode imported state: %v", diags)
	}
	if imported.Source.ValueString() != target {
		t.Fatalf("imported source got %q, want %q", imported.Source.ValueString(), target)
	}

	readResp := resource.ReadResponse{State: importResp.State}
	r.Read(ctx, resource.ReadRequest{State: importResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResp.Diagnostics)
	}

	var got HostLinkResourceModel
	if diags := readResp.State.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode refreshed state: %v", diags)
	}
	if got.ID.ValueString() != "~/.config/current" {
		t.Fatalf("id got %q", got.ID.ValueString())
	}
	if got.SourcePath.ValueString() != target {
		t.Fatalf("source_path got %q, want %q", got.SourcePath.ValueString(), target)
	}
	if got.DestinationPath.ValueString() != link {
		t.Fatalf("destination_path got %q, want %q", got.DestinationPath.ValueString(), link)
	}
}

func TestHostLinkResourceImportStateRejectsMissingLink(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	r := &HostLinkResource{homeDir: t.TempDir()}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	importResp := resource.ImportStateResponse{
		State: tfsdk.State{
			Schema: schemaResp.Schema,
			Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
		},
	}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "~/.config/missing"}, &importResp)
	if !importResp.Diagnostics.HasError() {
		t.Fatal("expected missing link import error")
	}
}
