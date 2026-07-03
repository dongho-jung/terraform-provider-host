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
	_ resource.Resource                 = &HostFileBlockResource{}
	_ resource.ResourceWithUpgradeState = &HostFileBlockResource{}
)

type HostFileBlockResource struct {
}

type HostFileBlockResourceModel struct {
	ID        types.String                 `tfsdk:"id"`
	Block     *HostFileBlockReferenceModel `tfsdk:"block"`
	FileBlock *HostFileBlockReferenceModel `tfsdk:"file_block"`
	Before    types.List                   `tfsdk:"before"`
	After     types.List                   `tfsdk:"after"`
	Content   types.String                 `tfsdk:"content"`
}

type hostFileBlockResourceModelV0 struct {
	ID        types.String                   `tfsdk:"id"`
	File      types.String                   `tfsdk:"file"`
	Block     types.String                   `tfsdk:"block"`
	Render    types.String                   `tfsdk:"render"`
	FileBlock *hostFileBlockReferenceModelV1 `tfsdk:"file_block"`
	Before    types.List                     `tfsdk:"before"`
	After     types.List                     `tfsdk:"after"`
	Priority  types.Int64                    `tfsdk:"priority"`
	Content   types.String                   `tfsdk:"content"`
}

type hostFileBlockResourceModelV1 struct {
	ID        types.String                   `tfsdk:"id"`
	Block     *hostFileBlockReferenceModelV1 `tfsdk:"block"`
	FileBlock *hostFileBlockReferenceModelV1 `tfsdk:"file_block"`
	Before    types.List                     `tfsdk:"before"`
	After     types.List                     `tfsdk:"after"`
	Content   types.String                   `tfsdk:"content"`
}

type hostFileBlockReferenceModelV1 struct {
	Name   types.String `tfsdk:"name"`
	Path   types.String `tfsdk:"path"`
	Render types.String `tfsdk:"render"`
}

type hostFileBlockTarget struct {
	path string
	name string
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
				Optional:            true,
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
				},
			},
			"file_block": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Legacy target file block reference. Prefer `block`.",
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Target file block name.",
					},
					"path": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Path to the host file that contains the target block.",
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

func (r *HostFileBlockResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: hostFileBlockResourceV0Schema(),
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior hostFileBlockResourceModelV0
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}

				upgraded := HostFileBlockResourceModel{
					ID:      prior.ID,
					Before:  prior.Before,
					After:   prior.After,
					Content: prior.Content,
				}
				if prior.FileBlock != nil {
					upgraded.Block = upgradeHostFileBlockReferenceV1(prior.FileBlock)
				} else if isKnownNonEmptyString(prior.File) && isKnownNonEmptyString(prior.Block) {
					upgraded.Block = &HostFileBlockReferenceModel{
						Name: prior.Block,
						Path: prior.File,
					}
				}

				resp.Diagnostics.Append(resp.State.Set(ctx, &upgraded)...)
			},
		},
		1: {
			PriorSchema: hostFileBlockResourceV1Schema(),
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior hostFileBlockResourceModelV1
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}

				upgraded := HostFileBlockResourceModel{
					ID:        prior.ID,
					Block:     upgradeHostFileBlockReferenceV1(prior.Block),
					FileBlock: upgradeHostFileBlockReferenceV1(prior.FileBlock),
					Before:    prior.Before,
					After:     prior.After,
					Content:   prior.Content,
				}

				resp.Diagnostics.Append(resp.State.Set(ctx, &upgraded)...)
			},
		},
	}
}

func upgradeHostFileBlockReferenceV1(ref *hostFileBlockReferenceModelV1) *HostFileBlockReferenceModel {
	if ref == nil {
		return nil
	}

	return &HostFileBlockReferenceModel{
		Name: ref.Name,
		Path: ref.Path,
	}
}

func hostFileBlockResourceV0Schema() *schema.Schema {
	return &schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"file": schema.StringAttribute{
				Optional: true,
			},
			"block": schema.StringAttribute{
				Optional: true,
			},
			"render": schema.StringAttribute{
				Optional: true,
				Computed: true,
			},
			"file_block": schema.SingleNestedAttribute{
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Required: true,
					},
					"path": schema.StringAttribute{
						Required: true,
					},
					"render": schema.StringAttribute{
						Optional: true,
						Computed: true,
					},
				},
			},
			"content": schema.StringAttribute{
				Required: true,
			},
			"before": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
			},
			"after": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
			},
			"priority": schema.Int64Attribute{
				Optional: true,
				Computed: true,
			},
		},
	}
}

func hostFileBlockResourceV1Schema() *schema.Schema {
	return &schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"block": schema.SingleNestedAttribute{
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Required: true,
					},
					"path": schema.StringAttribute{
						Required: true,
					},
					"render": schema.StringAttribute{
						Optional: true,
						Computed: true,
					},
				},
			},
			"file_block": schema.SingleNestedAttribute{
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Required: true,
					},
					"path": schema.StringAttribute{
						Required: true,
					},
					"render": schema.StringAttribute{
						Optional: true,
						Computed: true,
					},
				},
			},
			"content": schema.StringAttribute{
				Required: true,
			},
			"before": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
			},
			"after": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
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
	if _, err := expandHostPath(ref.Path.ValueString()); err != nil {
		return err
	}
	if err := validateHostFileBlockName(ref.Name.ValueString()); err != nil {
		return err
	}
	return nil
}

func hostFileBlockTargetFromModel(model HostFileBlockResourceModel) (hostFileBlockTarget, diag.Diagnostics) {
	var diags diag.Diagnostics
	hasNewTarget := model.Block != nil && (isKnownNonEmptyString(model.Block.Path) || isKnownNonEmptyString(model.Block.Name))
	hasLegacyTarget := model.FileBlock != nil && (isKnownNonEmptyString(model.FileBlock.Path) || isKnownNonEmptyString(model.FileBlock.Name))
	if hasNewTarget && hasLegacyTarget {
		diags.AddError("Invalid host file block target", "`block` and `file_block` are mutually exclusive.")
		return hostFileBlockTarget{}, diags
	}

	if hasNewTarget {
		if err := validateHostFileBlockReference(*model.Block); err != nil {
			diags.AddError("Invalid host file block reference", err.Error())
			return hostFileBlockTarget{}, diags
		}

		return hostFileBlockTarget{
			path: model.Block.Path.ValueString(),
			name: model.Block.Name.ValueString(),
		}, diags
	}

	if model.FileBlock == nil {
		diags.AddError("Invalid host file block target", "Either `file` and `block`, or legacy `file_block`, must be set.")
		return hostFileBlockTarget{}, diags
	}
	if err := validateHostFileBlockReference(*model.FileBlock); err != nil {
		diags.AddError("Invalid host file block reference", err.Error())
		return hostFileBlockTarget{}, diags
	}

	return hostFileBlockTarget{
		path: model.FileBlock.Path.ValueString(),
		name: model.FileBlock.Name.ValueString(),
	}, diags
}

func hostFileBlockTargetChanged(a hostFileBlockTarget, b hostFileBlockTarget) bool {
	return a.path != b.path || a.name != b.name
}

func isKnownNonEmptyString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown() && value.ValueString() != ""
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
