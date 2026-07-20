package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &HostFileBlockResource{}
	_ resource.ResourceWithConfigure   = &HostFileBlockResource{}
	_ resource.ResourceWithImportState = &HostFileBlockResource{}
)

type HostFileBlockResource struct {
	homeDir    string
	runtimeDir string
}

type HostFileBlockResourceModel struct {
	ID      types.String                 `tfsdk:"id"`
	Block   *HostFileBlockReferenceModel `tfsdk:"block"`
	Before  types.List                   `tfsdk:"before"`
	After   types.List                   `tfsdk:"after"`
	Content types.String                 `tfsdk:"content"`
}

type hostFileBlockTarget struct {
	path         string
	pathResolved string
	name         string
}

func NewHostFileBlockResource() resource.Resource {
	return &HostFileBlockResource{}
}

func (r *HostFileBlockResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file_block"
}

func (r *HostFileBlockResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(HostProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected HostProviderData, got %T.", req.ProviderData))
		return
	}
	r.homeDir = data.HomeDir
	r.runtimeDir = data.RuntimeDir
}

func (r *HostFileBlockResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             2,
		MarkdownDescription: "Manages one Terraform-owned content block inside a named `host_file` block.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Stable identifier for this content block.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"block": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Target file block reference, usually `host_file.<name>.blocks.<block>`.",
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Target file block name.",
					},
					"path": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Path to the host file that contains the target block.",
					},
					"path_resolved": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "Resolved absolute path to the host file that contains the target block.",
					},
				},
			},
			"content": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Content to place inside the target file block.",
			},
			"before": schema.ListAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				MarkdownDescription: "IDs of sibling `host_file_block` resources that this block must be rendered before.",
			},
			"after": schema.ListAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				MarkdownDescription: "IDs of sibling `host_file_block` resources that this block must be rendered after.",
			},
		},
	}
}

func (r *HostFileBlockResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostFileBlockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	blockID, err := newHostFileManagedBlockID()
	if err != nil {
		resp.Diagnostics.AddError("Failed to create host file block ID", err.Error())
		return
	}
	plan.ID = types.StringValue(blockID)

	target, targetDiags := hostFileBlockTargetFromModelForHome(plan, r.homeDir)
	resp.Diagnostics.Append(targetDiags...)
	before, beforeDiags := stringListValue(ctx, plan.Before, "host file block before")
	resp.Diagnostics.Append(beforeDiags...)
	after, afterDiags := stringListValue(ctx, plan.After, "host file block after")
	resp.Diagnostics.Append(afterDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	hydrateHostFileBlockReference(&plan, target)

	if err := withLockedHostFileForHome(ctx, r.homeDir, target.path, func(path string) error {
		return upsertCleanHostFileManagedBlockWithOrderForRuntime(path, target.name, blockID, before, after, plan.Content.ValueString(), r.runtimeDir)
	}); err != nil {
		resp.Diagnostics.AddError("Failed to sync host file block", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *HostFileBlockResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostFileBlockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validateHostFileManagedBlockID(state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid host file block ID", err.Error())
		return
	}
	target, targetDiags := hostFileBlockTargetFromModelForHome(state, r.homeDir)
	resp.Diagnostics.Append(targetDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var block hostFileManagedBlock
	var exists bool
	if err := withLockedHostFileForHome(ctx, r.homeDir, target.path, func(path string) error {
		var err error
		block, exists, err = readCleanManagedBlockForRuntime(path, target.name, state.ID.ValueString(), r.runtimeDir)
		return err
	}); err != nil {
		resp.Diagnostics.AddError("Failed to read host file block", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	if block.body != canonicalManagedBlockBody(state.Content.ValueString()) {
		state.Content = types.StringValue(trimRenderedManagedBlockBody(block.body))
	}
	if !sameStringListValue(ctx, state.Before, block.before) {
		state.Before = stringListStateValue(ctx, block.before, &resp.Diagnostics)
	}
	if !sameStringListValue(ctx, state.After, block.after) {
		state.After = stringListStateValue(ctx, block.after, &resp.Diagnostics)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	hydrateHostFileBlockReference(&state, target)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostFileBlockResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostFileBlockResourceModel
	var state HostFileBlockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	blockID := state.ID.ValueString()
	if blockID == "" {
		var err error
		blockID, err = newHostFileManagedBlockID()
		if err != nil {
			resp.Diagnostics.AddError("Failed to create host file block ID", err.Error())
			return
		}
	}
	plan.ID = types.StringValue(blockID)

	planTarget, planTargetDiags := hostFileBlockTargetFromModelForHome(plan, r.homeDir)
	resp.Diagnostics.Append(planTargetDiags...)
	stateTarget, stateTargetDiags := hostFileBlockTargetFromModelForHome(state, r.homeDir)
	resp.Diagnostics.Append(stateTargetDiags...)
	before, beforeDiags := stringListValue(ctx, plan.Before, "host file block before")
	resp.Diagnostics.Append(beforeDiags...)
	after, afterDiags := stringListValue(ctx, plan.After, "host file block after")
	resp.Diagnostics.Append(afterDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	hydrateHostFileBlockReference(&plan, planTarget)

	if hostFileBlockTargetChanged(planTarget, stateTarget) {
		if err := withLockedHostFileForHome(ctx, r.homeDir, stateTarget.path, func(path string) error {
			return removeCleanHostFileManagedBlockForRuntime(path, stateTarget.name, blockID, r.runtimeDir)
		}); err != nil {
			resp.Diagnostics.AddError("Failed to remove prior host file block", err.Error())
			return
		}
	}

	if err := withLockedHostFileForHome(ctx, r.homeDir, planTarget.path, func(path string) error {
		return upsertCleanHostFileManagedBlockWithOrderForRuntime(path, planTarget.name, blockID, before, after, plan.Content.ValueString(), r.runtimeDir)
	}); err != nil {
		resp.Diagnostics.AddError("Failed to sync host file block", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *HostFileBlockResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostFileBlockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validateHostFileManagedBlockID(state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid host file block ID", err.Error())
		return
	}
	target, targetDiags := hostFileBlockTargetFromModelForHome(state, r.homeDir)
	resp.Diagnostics.Append(targetDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := withLockedHostFileForHome(ctx, r.homeDir, target.path, func(path string) error {
		return removeCleanHostFileManagedBlockForRuntime(path, target.name, state.ID.ValueString(), r.runtimeDir)
	}); err != nil {
		resp.Diagnostics.AddError("Failed to delete host file block", err.Error())
	}
}

// ImportState imports one managed content block as `<path>:<block name>:<block id>`.
// The block ID is the `hfb-…` identifier recorded for the block in the provider
// runtime state under `<runtime_dir>/host_files/` (normally
// `~/.local/state/terraform-provider-host/host_files/` for a new
// configuration, or the legacy `./.terraform-provider-host/host_files/`). Content and
// ordering are hydrated by the follow-up Read.
func (r *HostFileBlockResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	filePath, blockName, blockID, err := parseHostFileBlockImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid host file block import ID", err.Error())
		return
	}

	state := HostFileBlockResourceModel{
		ID: types.StringValue(blockID),
		Block: &HostFileBlockReferenceModel{
			Name:         types.StringValue(blockName),
			Path:         types.StringValue(filePath),
			PathResolved: types.StringNull(),
		},
		Before:  types.ListNull(types.StringType),
		After:   types.ListNull(types.StringType),
		Content: types.StringNull(),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// parseHostFileBlockImportID splits `<path>:<block name>:<block id>` anchored
// on the last two separators, so file paths containing `:` still parse. Block
// names containing `:` cannot be imported with this format.
func parseHostFileBlockImportID(importID string) (string, string, string, error) {
	formatErr := fmt.Errorf("import ID must be `<path>:<block name>:<block id>`, where the block ID is the `hfb-…` identifier recorded under `<runtime_dir>/host_files/` (normally `~/.local/state/terraform-provider-host/host_files/`, or the legacy `./.terraform-provider-host/host_files/`); got %q", importID)

	idSep := strings.LastIndex(importID, ":")
	if idSep < 0 {
		return "", "", "", formatErr
	}
	blockID := importID[idSep+1:]
	rest := importID[:idSep]

	nameSep := strings.LastIndex(rest, ":")
	if nameSep < 0 {
		return "", "", "", formatErr
	}
	blockName := rest[nameSep+1:]
	filePath := rest[:nameSep]

	if filePath == "" || !strings.HasPrefix(blockID, "hfb-") {
		return "", "", "", formatErr
	}
	if err := validateHostFileManagedBlockID(blockID); err != nil {
		return "", "", "", err
	}
	if err := validateHostFileBlockName(blockName); err != nil {
		return "", "", "", err
	}

	return filePath, blockName, blockID, nil
}

func validateHostFileBlockReferenceForHome(ref HostFileBlockReferenceModel, homeDir string) error {
	if ref.Path.IsNull() || ref.Path.IsUnknown() {
		return fmt.Errorf("path must be known")
	}
	if ref.Name.IsNull() || ref.Name.IsUnknown() {
		return fmt.Errorf("name must be known")
	}
	resolvedPath, err := expandHostPathWithHome(ref.Path.ValueString(), homeDir)
	if err != nil {
		return err
	}
	if !ref.PathResolved.IsNull() && !ref.PathResolved.IsUnknown() && ref.PathResolved.ValueString() != resolvedPath {
		return fmt.Errorf("path_resolved %q does not match resolved path %q", ref.PathResolved.ValueString(), resolvedPath)
	}
	if err := validateHostFileBlockName(ref.Name.ValueString()); err != nil {
		return err
	}
	return nil
}

func hostFileBlockTargetFromModelForHome(model HostFileBlockResourceModel, homeDir string) (hostFileBlockTarget, diag.Diagnostics) {
	var diags diag.Diagnostics
	if model.Block == nil {
		diags.AddError("Invalid host file block target", "`block` must be set.")
		return hostFileBlockTarget{}, diags
	}
	if err := validateHostFileBlockReferenceForHome(*model.Block, homeDir); err != nil {
		diags.AddError("Invalid host file block reference", err.Error())
		return hostFileBlockTarget{}, diags
	}
	resolvedPath, err := expandHostPathWithHome(model.Block.Path.ValueString(), homeDir)
	if err != nil {
		diags.AddError("Invalid host file block reference", err.Error())
		return hostFileBlockTarget{}, diags
	}

	return hostFileBlockTarget{
		path:         model.Block.Path.ValueString(),
		pathResolved: resolvedPath,
		name:         model.Block.Name.ValueString(),
	}, diags
}

func hydrateHostFileBlockReference(model *HostFileBlockResourceModel, target hostFileBlockTarget) {
	if model.Block == nil {
		return
	}
	model.Block.PathResolved = types.StringValue(target.pathResolved)
}

func hostFileBlockTargetChanged(a hostFileBlockTarget, b hostFileBlockTarget) bool {
	return a.pathResolved != b.pathResolved || a.name != b.name
}

func sameStringListValue(ctx context.Context, value types.List, expected []string) bool {
	actual, diags := stringListValue(ctx, value, "host file block ordering")
	if diags.HasError() {
		return false
	}
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return false
		}
	}

	return true
}

func stringListStateValue(ctx context.Context, elements []string, diags *diag.Diagnostics) types.List {
	if len(elements) == 0 {
		return types.ListNull(types.StringType)
	}

	value, valueDiags := types.ListValueFrom(ctx, types.StringType, elements)
	diags.Append(valueDiags...)
	if diags.HasError() {
		return types.ListUnknown(types.StringType)
	}

	return value
}

func newHostFileManagedBlockID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return "hfb-" + hex.EncodeToString(bytes[:]), nil
}
