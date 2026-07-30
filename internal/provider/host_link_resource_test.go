package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
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
