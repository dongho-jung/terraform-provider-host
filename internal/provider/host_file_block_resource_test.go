package provider

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestParseHostFileBlockImportID(t *testing.T) {
	t.Parallel()

	blockID := "hfb-" + strings.Repeat("0123", 8)

	cases := []struct {
		name     string
		importID string
		wantPath string
		wantName string
		wantID   string
		wantErr  bool
	}{
		{"basic", "~/.zshrc:alias:" + blockID, "~/.zshrc", "alias", blockID, false},
		{"path with colon", "/tmp/a:b/file:alias:" + blockID, "/tmp/a:b/file", "alias", blockID, false},
		{"missing block name", "~/.zshrc:" + blockID, "", "", "", true},
		{"missing hfb prefix", "~/.zshrc:alias:" + strings.Repeat("0123", 8), "", "", "", true},
		{"empty path", ":alias:" + blockID, "", "", "", true},
		{"empty", "", "", "", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path, name, id, err := parseHostFileBlockImportID(tc.importID)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got path=%q name=%q id=%q", tc.importID, path, name, id)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if path != tc.wantPath || name != tc.wantName || id != tc.wantID {
				t.Fatalf("parsed (%q, %q, %q), want (%q, %q, %q)", path, name, id, tc.wantPath, tc.wantName, tc.wantID)
			}
		})
	}
}

func TestHostFileBlockImportStatePopulatesTarget(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	r := &HostFileBlockResource{}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("unexpected schema diagnostics: %v", schemaResp.Diagnostics)
	}

	blockID := "hfb-" + strings.Repeat("ab01", 8)
	resp := &resource.ImportStateResponse{
		State: tfsdk.State{
			Schema: schemaResp.Schema,
			Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
		},
	}

	r.ImportState(ctx, resource.ImportStateRequest{ID: "~/.zshrc:alias:" + blockID}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var state HostFileBlockResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected state diagnostics: %v", resp.Diagnostics)
	}

	if state.ID.ValueString() != blockID {
		t.Fatalf("expected id %q, got %q", blockID, state.ID.ValueString())
	}
	if state.Block == nil {
		t.Fatal("expected block reference to be set")
	}
	if state.Block.Path.ValueString() != "~/.zshrc" {
		t.Fatalf("expected block path ~/.zshrc, got %q", state.Block.Path.ValueString())
	}
	if state.Block.Name.ValueString() != "alias" {
		t.Fatalf("expected block name alias, got %q", state.Block.Name.ValueString())
	}
	if !state.Content.IsNull() {
		t.Fatalf("expected content to be null before Read, got %#v", state.Content)
	}
}

func TestHostFileBlockImportStateRejectsInvalidID(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	r := &HostFileBlockResource{}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	resp := &resource.ImportStateResponse{
		State: tfsdk.State{
			Schema: schemaResp.Schema,
			Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
		},
	}

	r.ImportState(ctx, resource.ImportStateRequest{ID: "just-a-path"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics error for malformed import ID")
	}
}
