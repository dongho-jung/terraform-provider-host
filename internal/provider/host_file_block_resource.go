package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &HostFileBlockResource{}

type HostFileBlockResource struct {
}

type HostFileBlockResourceModel struct {
	ID        types.String                `tfsdk:"id"`
	FileBlock HostFileBlockReferenceModel `tfsdk:"file_block"`
	Priority  types.Int64                 `tfsdk:"priority"`
	Content   types.String                `tfsdk:"content"`
}

func NewHostFileBlockResource() resource.Resource {
	return &HostFileBlockResource{}
}

func (r *HostFileBlockResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file_block"
}

func (r *HostFileBlockResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one Terraform-owned content block inside a named `host_file` section.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Stable identifier for this content block.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"file_block": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Target file section, usually `host_file.<name>.block[\"section\"]`.",
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Target file section name.",
					},
					"path": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Path to the host file that contains the target section.",
					},
					"render": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						Default:             stringdefault.StaticString(hostFileRenderMarkers),
						MarkdownDescription: "Render mode inherited from the target host file section.",
					},
				},
			},
			"content": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Content to place inside the target file section.",
			},
			"priority": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
				MarkdownDescription: "Sort priority within the target file section. Blocks are ordered by `priority`, then `content`.",
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

	if err := validateHostFileBlockReference(plan.FileBlock); err != nil {
		resp.Diagnostics.AddError("Invalid host file block reference", err.Error())
		return
	}

	if err := withLockedHostFile(ctx, plan.FileBlock.Path.ValueString(), func(path string) error {
		return upsertHostFileManagedBlockByRender(path, hostFileBlockRender(plan.FileBlock), plan.FileBlock.Name.ValueString(), blockID, hostFileBlockPriority(plan), plan.Content.ValueString())
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

	if err := validateHostFileBlockReference(state.FileBlock); err != nil {
		resp.Diagnostics.AddError("Invalid host file block reference", err.Error())
		return
	}
	if err := validateHostFileManagedBlockID(state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid host file block ID", err.Error())
		return
	}

	var body string
	var priority int64
	var exists bool
	if err := withLockedHostFile(ctx, state.FileBlock.Path.ValueString(), func(path string) error {
		var err error
		body, priority, exists, err = readManagedBlockBodyByRender(path, hostFileBlockRender(state.FileBlock), state.FileBlock.Name.ValueString(), state.ID.ValueString())
		return err
	}); err != nil {
		resp.Diagnostics.AddError("Failed to read host file block", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	if body != canonicalManagedBlockBody(state.Content.ValueString()) {
		state.Content = types.StringValue(trimRenderedManagedBlockBody(body))
	}
	if state.Priority.IsNull() || state.Priority.IsUnknown() || priority != hostFileBlockPriority(state) {
		state.Priority = types.Int64Value(priority)
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

	if err := validateHostFileBlockReference(plan.FileBlock); err != nil {
		resp.Diagnostics.AddError("Invalid host file block reference", err.Error())
		return
	}
	if err := validateHostFileBlockReference(state.FileBlock); err != nil {
		resp.Diagnostics.AddError("Invalid prior host file block reference", err.Error())
		return
	}

	if hostFileBlockReferenceChanged(plan.FileBlock, state.FileBlock) {
		if err := withLockedHostFile(ctx, state.FileBlock.Path.ValueString(), func(path string) error {
			return removeHostFileManagedBlockByRender(path, hostFileBlockRender(state.FileBlock), state.FileBlock.Name.ValueString(), blockID)
		}); err != nil {
			resp.Diagnostics.AddError("Failed to remove prior host file block", err.Error())
			return
		}
	}

	if err := withLockedHostFile(ctx, plan.FileBlock.Path.ValueString(), func(path string) error {
		return upsertHostFileManagedBlockByRender(path, hostFileBlockRender(plan.FileBlock), plan.FileBlock.Name.ValueString(), blockID, hostFileBlockPriority(plan), plan.Content.ValueString())
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

	if err := validateHostFileBlockReference(state.FileBlock); err != nil {
		resp.Diagnostics.AddError("Invalid host file block reference", err.Error())
		return
	}
	if err := validateHostFileManagedBlockID(state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid host file block ID", err.Error())
		return
	}

	if err := withLockedHostFile(ctx, state.FileBlock.Path.ValueString(), func(path string) error {
		return removeHostFileManagedBlockByRender(path, hostFileBlockRender(state.FileBlock), state.FileBlock.Name.ValueString(), state.ID.ValueString())
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
	if err := validateHostFileRender(hostFileBlockRender(ref)); err != nil {
		return err
	}

	return nil
}

func hostFileBlockReferenceChanged(a HostFileBlockReferenceModel, b HostFileBlockReferenceModel) bool {
	return a.Path.ValueString() != b.Path.ValueString() ||
		a.Name.ValueString() != b.Name.ValueString() ||
		hostFileBlockRender(a) != hostFileBlockRender(b)
}

func hostFileBlockPriority(model HostFileBlockResourceModel) int64 {
	if model.Priority.IsNull() || model.Priority.IsUnknown() {
		return 0
	}

	return model.Priority.ValueInt64()
}

func hostFileBlockRender(ref HostFileBlockReferenceModel) string {
	if ref.Render.IsNull() || ref.Render.IsUnknown() || ref.Render.ValueString() == "" {
		return hostFileRenderMarkers
	}

	return ref.Render.ValueString()
}

func upsertHostFileManagedBlockByRender(path string, render string, fileBlockName string, blockID string, priority int64, content string) error {
	if render == hostFileRenderClean {
		return upsertCleanHostFileManagedBlock(path, fileBlockName, blockID, priority, content)
	}

	return upsertHostFileManagedBlock(path, fileBlockName, blockID, priority, content)
}

func removeHostFileManagedBlockByRender(path string, render string, fileBlockName string, blockID string) error {
	if render == hostFileRenderClean {
		return removeCleanHostFileManagedBlock(path, fileBlockName, blockID)
	}

	return removeHostFileManagedBlock(path, fileBlockName, blockID)
}

func readManagedBlockBodyByRender(path string, render string, fileBlockName string, blockID string) (string, int64, bool, error) {
	if render == hostFileRenderClean {
		return readCleanManagedBlockBody(path, fileBlockName, blockID)
	}

	return readManagedBlockBody(path, fileBlockName, blockID)
}

func newHostFileManagedBlockID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return "hfb-" + hex.EncodeToString(bytes[:]), nil
}
