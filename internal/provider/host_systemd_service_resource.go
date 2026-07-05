package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &HostSystemdServiceResource{}
	_ resource.ResourceWithConfigure   = &HostSystemdServiceResource{}
	_ resource.ResourceWithImportState = &HostSystemdServiceResource{}
	_ resource.ResourceWithModifyPlan  = &HostSystemdServiceResource{}
)

type HostSystemdServiceResource struct {
	manager SystemdServiceManager
}

type HostSystemdServiceResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Enabled types.Bool   `tfsdk:"enabled"`
	Running types.Bool   `tfsdk:"running"`
}

func NewHostSystemdServiceResource() resource.Resource {
	return &HostSystemdServiceResource{}
}

func (r *HostSystemdServiceResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_systemd_service"
}

func (r *HostSystemdServiceResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one systemd service unit's enabled and running state.",
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
				MarkdownDescription: "Systemd service unit name. Must end with `.service`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"enabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Whether the service should be enabled at boot.",
			},
			"running": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Whether the service should be running now.",
			},
		},
	}
}

func (r *HostSystemdServiceResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.SystemdManager == nil {
			resp.Diagnostics.AddError("systemd backend unavailable", "`host_systemd_service` requires `systemctl`.")
			return
		}
		r.manager = data.SystemdManager
	case SystemdServiceManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or SystemdServiceManager, got %T.", req.ProviderData),
		)
	}
}

func (r *HostSystemdServiceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostSystemdServiceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !hostSystemdServicePlanReady(plan) {
		return
	}

	if err := validateSystemdServiceName(plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid systemd service", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.Name.ValueString())
	if hostSystemdServicePlanRequiresMutationWarning(ctx, req.State, plan, &resp.Diagnostics) {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostSystemdServiceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostSystemdServiceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SyncService(ctx, systemdServiceSpecFromModel(plan)); err != nil {
		resp.Diagnostics.AddError("Failed to sync systemd service", err.Error())
		return
	}

	state, exists, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read systemd service", err.Error())
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Failed to read systemd service", fmt.Sprintf("Service %q was not found after sync.", plan.Name.ValueString()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSystemdServiceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostSystemdServiceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, exists, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read systemd service", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostSystemdServiceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostSystemdServiceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SyncService(ctx, systemdServiceSpecFromModel(plan)); err != nil {
		resp.Diagnostics.AddError("Failed to sync systemd service", err.Error())
		return
	}

	state, exists, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read systemd service", err.Error())
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Failed to read systemd service", fmt.Sprintf("Service %q was not found after sync.", plan.Name.ValueString()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSystemdServiceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
}

func (r *HostSystemdServiceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *HostSystemdServiceResource) refreshState(ctx context.Context, model HostSystemdServiceResourceModel) (HostSystemdServiceResourceModel, bool, error) {
	status, err := r.manager.ServiceStatus(ctx, model.Name.ValueString())
	if err != nil {
		return model, false, err
	}
	if !status.Exists {
		return model, false, nil
	}

	model.ID = types.StringValue(status.Name)
	model.Name = types.StringValue(status.Name)
	model.Enabled = types.BoolValue(status.Enabled)
	model.Running = types.BoolValue(status.Running)
	return model, true, nil
}

func (r *HostSystemdServiceResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	if r.manager == nil || !r.manager.NeedsPrivilegeEscalation() {
		return
	}

	addSudoPrivilegeWarningOnce(diags)
}

func hostSystemdServicePlanReady(model HostSystemdServiceResourceModel) bool {
	return !model.Name.IsNull() && !model.Name.IsUnknown() &&
		!model.Enabled.IsNull() && !model.Enabled.IsUnknown() &&
		!model.Running.IsNull() && !model.Running.IsUnknown()
}

func systemdServiceSpecFromModel(model HostSystemdServiceResourceModel) SystemdServiceSpec {
	return SystemdServiceSpec{
		Name:    model.Name.ValueString(),
		Enabled: model.Enabled.ValueBool(),
		Running: model.Running.ValueBool(),
	}
}

func hostSystemdServicePlanRequiresMutationWarning(ctx context.Context, stateData tfsdk.State, plan HostSystemdServiceResourceModel, diags *diag.Diagnostics) bool {
	if stateData.Raw.IsNull() {
		return true
	}

	var state HostSystemdServiceResourceModel
	diags.Append(stateData.Get(ctx, &state)...)
	if diags.HasError() || !hostSystemdServicePlanReady(state) {
		return false
	}

	return plan.Name.ValueString() != state.Name.ValueString() ||
		plan.Enabled.ValueBool() != state.Enabled.ValueBool() ||
		plan.Running.ValueBool() != state.Running.ValueBool()
}
