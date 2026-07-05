package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &HostFstabEntryResource{}
	_ resource.ResourceWithConfigure   = &HostFstabEntryResource{}
	_ resource.ResourceWithImportState = &HostFstabEntryResource{}
	_ resource.ResourceWithModifyPlan  = &HostFstabEntryResource{}
)

type HostFstabEntryResource struct {
	manager FstabManager
}

type HostFstabEntryResourceModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Device     types.String `tfsdk:"device"`
	MountPoint types.String `tfsdk:"mount_point"`
	FSType     types.String `tfsdk:"fs_type"`
	Options    types.String `tfsdk:"options"`
	Dump       types.Int64  `tfsdk:"dump"`
	Pass       types.Int64  `tfsdk:"pass"`
	Path       types.String `tfsdk:"path"`
}

func NewHostFstabEntryResource() resource.Resource {
	return &HostFstabEntryResource{}
}

func (r *HostFstabEntryResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fstab_entry"
}

func (r *HostFstabEntryResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one provider-owned entry block in `/etc/fstab`. It does not mount or unmount filesystems.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, equal to `name`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Stable name for this managed fstab entry block.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"device": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Fstab device field, such as `UUID=...` or `/dev/disk/by-label/data`.",
			},
			"mount_point": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Mount point field. Use an absolute path or `none`.",
			},
			"fs_type": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Filesystem type field, such as `ext4`, `btrfs`, `xfs`, or `swap`.",
			},
			"options": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("defaults"),
				MarkdownDescription: "Fstab options field.",
			},
			"dump": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
				MarkdownDescription: "Fstab dump field.",
			},
			"pass": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
				MarkdownDescription: "Fstab pass field.",
			},
			"path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Managed fstab path.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *HostFstabEntryResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.FstabManager == nil {
			resp.Diagnostics.AddError("Fstab backend unavailable", "`host_fstab_entry` requires local filesystem access to `/etc/fstab`.")
			return
		}
		r.manager = data.FstabManager
	case FstabManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or FstabManager, got %T.", req.ProviderData),
		)
	}
}

func (r *HostFstabEntryResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostFstabEntryResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !hostFstabEntryPlanReady(plan) {
		return
	}

	entry := fstabEntryFromModel(plan)
	if err := validateHostFstabEntry(entry); err != nil {
		resp.Diagnostics.AddError("Invalid fstab entry", err.Error())
		return
	}

	plan.ID = types.StringValue(entry.Name)
	plan.Path = types.StringValue(hostFstabPath)
	if hostFstabEntryPlanRequiresMutationWarning(ctx, req.State, plan, &resp.Diagnostics) {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostFstabEntryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostFstabEntryResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SyncEntry(ctx, fstabEntryFromModel(plan)); err != nil {
		resp.Diagnostics.AddError("Failed to sync fstab entry", err.Error())
		return
	}

	state, exists, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read fstab entry", err.Error())
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Failed to read fstab entry", fmt.Sprintf("Entry %q was not found after sync.", plan.Name.ValueString()))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostFstabEntryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostFstabEntryResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, exists, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read fstab entry", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostFstabEntryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostFstabEntryResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SyncEntry(ctx, fstabEntryFromModel(plan)); err != nil {
		resp.Diagnostics.AddError("Failed to sync fstab entry", err.Error())
		return
	}

	state, exists, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read fstab entry", err.Error())
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Failed to read fstab entry", fmt.Sprintf("Entry %q was not found after sync.", plan.Name.ValueString()))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostFstabEntryResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostFstabEntryResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.DeleteEntry(ctx, state.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to delete fstab entry", err.Error())
	}
}

func (r *HostFstabEntryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *HostFstabEntryResource) refreshState(ctx context.Context, model HostFstabEntryResourceModel) (HostFstabEntryResourceModel, bool, error) {
	entry, exists, err := r.manager.Entry(ctx, model.Name.ValueString())
	if err != nil || !exists {
		return model, exists, err
	}
	model.ID = types.StringValue(entry.Name)
	model.Name = types.StringValue(entry.Name)
	model.Device = types.StringValue(entry.Device)
	model.MountPoint = types.StringValue(entry.MountPoint)
	model.FSType = types.StringValue(entry.FSType)
	model.Options = types.StringValue(entry.Options)
	model.Dump = types.Int64Value(entry.Dump)
	model.Pass = types.Int64Value(entry.Pass)
	model.Path = types.StringValue(hostFstabPath)
	return model, true, nil
}

func (r *HostFstabEntryResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	if r.manager == nil || !r.manager.NeedsPrivilegeEscalation() {
		return
	}
	addSudoPrivilegeWarningOnce(diags)
}

func hostFstabEntryPlanReady(model HostFstabEntryResourceModel) bool {
	return !model.Name.IsNull() && !model.Name.IsUnknown() &&
		!model.Device.IsNull() && !model.Device.IsUnknown() &&
		!model.MountPoint.IsNull() && !model.MountPoint.IsUnknown() &&
		!model.FSType.IsNull() && !model.FSType.IsUnknown() &&
		!model.Options.IsNull() && !model.Options.IsUnknown() &&
		!model.Dump.IsNull() && !model.Dump.IsUnknown() &&
		!model.Pass.IsNull() && !model.Pass.IsUnknown()
}

func fstabEntryFromModel(model HostFstabEntryResourceModel) FstabEntry {
	return FstabEntry{
		Name:       model.Name.ValueString(),
		Device:     model.Device.ValueString(),
		MountPoint: model.MountPoint.ValueString(),
		FSType:     model.FSType.ValueString(),
		Options:    model.Options.ValueString(),
		Dump:       model.Dump.ValueInt64(),
		Pass:       model.Pass.ValueInt64(),
	}
}

func hostFstabEntryPlanRequiresMutationWarning(ctx context.Context, stateData tfsdk.State, plan HostFstabEntryResourceModel, diags *diag.Diagnostics) bool {
	if stateData.Raw.IsNull() {
		return true
	}

	var state HostFstabEntryResourceModel
	diags.Append(stateData.Get(ctx, &state)...)
	if diags.HasError() || !hostFstabEntryPlanReady(state) {
		return false
	}

	return plan.Name.ValueString() != state.Name.ValueString() ||
		plan.Device.ValueString() != state.Device.ValueString() ||
		plan.MountPoint.ValueString() != state.MountPoint.ValueString() ||
		plan.FSType.ValueString() != state.FSType.ValueString() ||
		plan.Options.ValueString() != state.Options.ValueString() ||
		plan.Dump.ValueInt64() != state.Dump.ValueInt64() ||
		plan.Pass.ValueInt64() != state.Pass.ValueInt64()
}
