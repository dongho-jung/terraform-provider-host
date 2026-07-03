package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource = &HostFileBlockResource{}
)

type HostFileBlockResource struct {
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

	target, targetDiags := hostFileBlockTargetFromModel(plan)
	resp.Diagnostics.Append(targetDiags...)
	before, beforeDiags := stringListValue(ctx, plan.Before, "host file block before")
	resp.Diagnostics.Append(beforeDiags...)
	after, afterDiags := stringListValue(ctx, plan.After, "host file block after")
	resp.Diagnostics.Append(afterDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	hydrateHostFileBlockReference(&plan, target)

	if err := withLockedHostFile(ctx, target.path, func(path string) error {
		return upsertCleanHostFileManagedBlockWithOrder(path, target.name, blockID, before, after, plan.Content.ValueString())
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
	target, targetDiags := hostFileBlockTargetFromModel(state)
	resp.Diagnostics.Append(targetDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var block hostFileManagedBlock
	var exists bool
	if err := withLockedHostFile(ctx, target.path, func(path string) error {
		var err error
		block, exists, err = readCleanManagedBlock(path, target.name, state.ID.ValueString())
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

	planTarget, planTargetDiags := hostFileBlockTargetFromModel(plan)
	resp.Diagnostics.Append(planTargetDiags...)
	stateTarget, stateTargetDiags := hostFileBlockTargetFromModel(state)
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
		if err := withLockedHostFile(ctx, stateTarget.path, func(path string) error {
			return removeCleanHostFileManagedBlock(path, stateTarget.name, blockID)
		}); err != nil {
			resp.Diagnostics.AddError("Failed to remove prior host file block", err.Error())
			return
		}
	}

	if err := withLockedHostFile(ctx, planTarget.path, func(path string) error {
		return upsertCleanHostFileManagedBlockWithOrder(path, planTarget.name, blockID, before, after, plan.Content.ValueString())
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
	target, targetDiags := hostFileBlockTargetFromModel(state)
	resp.Diagnostics.Append(targetDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := withLockedHostFile(ctx, target.path, func(path string) error {
		return removeCleanHostFileManagedBlock(path, target.name, state.ID.ValueString())
	}); err != nil {
		resp.Diagnostics.AddError("Failed to delete host file block", err.Error())
	}
}

func validateHostFileBlockReference(ref HostFileBlockReferenceModel) error {
	if ref.Path.IsNull() || ref.Path.IsUnknown() {
		return fmt.Errorf("path must be known")
	}
	if ref.Name.IsNull() || ref.Name.IsUnknown() {
		return fmt.Errorf("name must be known")
	}
	resolvedPath, err := expandHostPath(ref.Path.ValueString())
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

func hostFileBlockTargetFromModel(model HostFileBlockResourceModel) (hostFileBlockTarget, diag.Diagnostics) {
	var diags diag.Diagnostics
	if model.Block == nil {
		diags.AddError("Invalid host file block target", "`block` must be set.")
		return hostFileBlockTarget{}, diags
	}
	if err := validateHostFileBlockReference(*model.Block); err != nil {
		diags.AddError("Invalid host file block reference", err.Error())
		return hostFileBlockTarget{}, diags
	}
	resolvedPath, err := expandHostPath(model.Block.Path.ValueString())
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
	return a.path != b.path || a.name != b.name
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
