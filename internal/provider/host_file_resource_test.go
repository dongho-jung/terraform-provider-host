package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestHostFileResourceImportStateHydratesWholeFileBeforeRead(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	homeDir := t.TempDir()
	filePath := filepath.Join(homeDir, ".config", "example.conf")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("create file parent: %s", err)
	}
	const renderedContent = "first line\nsecond line\n"
	if err := os.WriteFile(filePath, []byte(renderedContent), 0o600); err != nil {
		t.Fatalf("write imported file: %s", err)
	}

	r := &HostFileResource{
		homeDir:    homeDir,
		runtimeDir: t.TempDir(),
	}
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
	r.ImportState(ctx, resource.ImportStateRequest{ID: "~/.config/example.conf"}, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", importResp.Diagnostics)
	}

	var imported HostFileResourceModel
	if diags := importResp.State.Get(ctx, &imported); diags.HasError() {
		t.Fatalf("decode imported state: %v", diags)
	}
	if imported.Content.ValueString() != "first line\nsecond line" {
		t.Fatalf("imported content got %q", imported.Content.ValueString())
	}

	readResp := resource.ReadResponse{State: importResp.State}
	r.Read(ctx, resource.ReadRequest{State: importResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResp.Diagnostics)
	}

	var got HostFileResourceModel
	if diags := readResp.State.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode refreshed state: %v", diags)
	}
	if got.ID.ValueString() != "~/.config/example.conf" {
		t.Fatalf("id got %q", got.ID.ValueString())
	}
	if got.PathResolved.ValueString() != filePath {
		t.Fatalf("path_resolved got %q, want %q", got.PathResolved.ValueString(), filePath)
	}
	if got.RenderedContent.ValueString() != renderedContent {
		t.Fatalf("rendered_content got %q", got.RenderedContent.ValueString())
	}
	if !got.Blocks.IsNull() {
		t.Fatalf("blocks got %#v, want null", got.Blocks)
	}
}

func TestHostFileResourceImportStateRejectsMissingFile(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	r := &HostFileResource{
		homeDir:    t.TempDir(),
		runtimeDir: t.TempDir(),
	}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	importResp := resource.ImportStateResponse{
		State: tfsdk.State{
			Schema: schemaResp.Schema,
			Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
		},
	}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "~/.config/missing.conf"}, &importResp)
	if !importResp.Diagnostics.HasError() {
		t.Fatal("expected missing file import error")
	}
}
