package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &HostSysctlResource{}
	_ resource.ResourceWithConfigure   = &HostSysctlResource{}
	_ resource.ResourceWithImportState = &HostSysctlResource{}
	_ resource.ResourceWithModifyPlan  = &HostSysctlResource{}
)

type HostSysctlResource struct {
	manager SysctlManager
}

type HostSysctlResourceModel struct {
	ID    types.String `tfsdk:"id"`
	Key   types.String `tfsdk:"key"`
	Value types.String `tfsdk:"value"`
	Path  types.String `tfsdk:"path"`
}

func NewHostSysctlResource() resource.Resource {
	return &HostSysctlResource{}
}

func (r *HostSysctlResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sysctl"
}

func (r *HostSysctlResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one Linux sysctl key through a provider-owned file in `/etc/sysctl.d` and applies the live value with `sysctl -w`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, equal to `key`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"key": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Sysctl key, such as `vm.swappiness`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"value": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Desired sysctl value.",
			},
			"path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Provider-owned sysctl configuration file path.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *HostSysctlResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.SysctlManager == nil {
			resp.Diagnostics.AddError("Sysctl backend unavailable", "`host_sysctl` requires `sysctl`.")
			return
		}
		r.manager = data.SysctlManager
	case SysctlManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or SysctlManager, got %T.", req.ProviderData),
		)
	}
}

func (r *HostSysctlResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostSysctlResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !hostSysctlPlanReady(plan) {
		return
	}

	spec := sysctlSpecFromModel(plan)
	if err := validateHostSysctlSpec(spec); err != nil {
		resp.Diagnostics.AddError("Invalid sysctl", err.Error())
		return
	}

	plan.ID = types.StringValue(spec.Key)
	plan.Path = types.StringValue(sysctlManagedPath(spec.Key))
	if hostSysctlPlanRequiresMutationWarning(ctx, req.State, plan, &resp.Diagnostics) {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostSysctlResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostSysctlResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SyncSysctl(ctx, sysctlSpecFromModel(plan)); err != nil {
		resp.Diagnostics.AddError("Failed to sync sysctl", err.Error())
		return
	}

	state, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read sysctl", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSysctlResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostSysctlResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read sysctl", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostSysctlResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostSysctlResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SyncSysctl(ctx, sysctlSpecFromModel(plan)); err != nil {
		resp.Diagnostics.AddError("Failed to sync sysctl", err.Error())
		return
	}

	state, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read sysctl", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSysctlResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostSysctlResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.DeleteSysctl(ctx, state.Key.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to delete sysctl file", err.Error())
	}
}

func (r *HostSysctlResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("key"), req, resp)
}

func (r *HostSysctlResource) refreshState(ctx context.Context, model HostSysctlResourceModel) (HostSysctlResourceModel, error) {
	value, err := r.manager.Sysctl(ctx, model.Key.ValueString())
	if err != nil {
		return model, err
	}
	model.ID = types.StringValue(model.Key.ValueString())
	model.Value = types.StringValue(value)
	model.Path = types.StringValue(sysctlManagedPath(model.Key.ValueString()))
	return model, nil
}

func (r *HostSysctlResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	if r.manager == nil || !r.manager.NeedsPrivilegeEscalation() {
		return
	}
	addSudoPrivilegeWarningOnce(diags)
}

func hostSysctlPlanReady(model HostSysctlResourceModel) bool {
	return !model.Key.IsNull() && !model.Key.IsUnknown() &&
		!model.Value.IsNull() && !model.Value.IsUnknown()
}

func sysctlSpecFromModel(model HostSysctlResourceModel) SysctlSpec {
	return SysctlSpec{
		Key:   model.Key.ValueString(),
		Value: model.Value.ValueString(),
	}
}

func hostSysctlPlanRequiresMutationWarning(ctx context.Context, stateData tfsdk.State, plan HostSysctlResourceModel, diags *diag.Diagnostics) bool {
	if stateData.Raw.IsNull() {
		return true
	}

	var state HostSysctlResourceModel
	diags.Append(stateData.Get(ctx, &state)...)
	if diags.HasError() || !hostSysctlPlanReady(state) {
		return false
	}

	return plan.Key.ValueString() != state.Key.ValueString() ||
		plan.Value.ValueString() != state.Value.ValueString()
}
