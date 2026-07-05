package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &HostSystemdUnitResource{}
	_ resource.ResourceWithConfigure   = &HostSystemdUnitResource{}
	_ resource.ResourceWithImportState = &HostSystemdUnitResource{}
	_ resource.ResourceWithModifyPlan  = &HostSystemdUnitResource{}
)

type HostSystemdUnitResource struct {
	manager SystemdUnitManager
}

type HostSystemdUnitResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Content types.String `tfsdk:"content"`
	Mode    types.String `tfsdk:"mode"`
	Path    types.String `tfsdk:"path"`
}

func NewHostSystemdUnitResource() resource.Resource {
	return &HostSystemdUnitResource{}
}

func (r *HostSystemdUnitResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_systemd_unit"
}

func (r *HostSystemdUnitResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one systemd unit file under `/etc/systemd/system` and runs `systemctl daemon-reload` after changes.",
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
				MarkdownDescription: "Systemd unit file name, such as `example.service` or `backup.timer`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"content": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Full systemd unit file content.",
			},
			"mode": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("0644"),
				MarkdownDescription: "Unit file permission mode as four octal digits.",
			},
			"path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unit file path under `/etc/systemd/system`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *HostSystemdUnitResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.SystemdUnitManager == nil {
			resp.Diagnostics.AddError("systemd unit backend unavailable", "`host_systemd_unit` requires `systemctl`.")
			return
		}
		r.manager = data.SystemdUnitManager
	case SystemdUnitManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or SystemdUnitManager, got %T.", req.ProviderData),
		)
	}
}

func (r *HostSystemdUnitResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostSystemdUnitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !hostSystemdUnitPlanReady(plan) {
		return
	}

	spec, err := systemdUnitSpecFromModel(plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid systemd unit", err.Error())
		return
	}
	if err := validateSystemdUnitSpec(spec); err != nil {
		resp.Diagnostics.AddError("Invalid systemd unit", err.Error())
		return
	}

	plan.ID = types.StringValue(spec.Name)
	plan.Path = types.StringValue(systemdUnitPath(spec.Name))
	if hostSystemdUnitPlanRequiresMutationWarning(ctx, req.State, plan, &resp.Diagnostics) {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostSystemdUnitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostSystemdUnitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	spec, err := systemdUnitSpecFromModel(plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid systemd unit", err.Error())
		return
	}
	if err := r.manager.SyncUnit(ctx, spec); err != nil {
		resp.Diagnostics.AddError("Failed to sync systemd unit", err.Error())
		return
	}

	state, exists, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read systemd unit", err.Error())
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Failed to read systemd unit", fmt.Sprintf("Unit %q was not found after sync.", plan.Name.ValueString()))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSystemdUnitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostSystemdUnitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, exists, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read systemd unit", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostSystemdUnitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostSystemdUnitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	spec, err := systemdUnitSpecFromModel(plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid systemd unit", err.Error())
		return
	}
	if err := r.manager.SyncUnit(ctx, spec); err != nil {
		resp.Diagnostics.AddError("Failed to sync systemd unit", err.Error())
		return
	}

	state, exists, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read systemd unit", err.Error())
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Failed to read systemd unit", fmt.Sprintf("Unit %q was not found after sync.", plan.Name.ValueString()))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSystemdUnitResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostSystemdUnitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.DeleteUnit(ctx, state.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to delete systemd unit", err.Error())
	}
}

func (r *HostSystemdUnitResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *HostSystemdUnitResource) refreshState(ctx context.Context, model HostSystemdUnitResourceModel) (HostSystemdUnitResourceModel, bool, error) {
	state, exists, err := r.manager.Unit(ctx, model.Name.ValueString())
	if err != nil || !exists {
		return model, exists, err
	}
	model.ID = types.StringValue(state.Name)
	model.Name = types.StringValue(state.Name)
	model.Content = types.StringValue(state.Content)
	model.Mode = types.StringValue(formatHostDirMode(state.Mode))
	model.Path = types.StringValue(state.Path)
	return model, true, nil
}

func (r *HostSystemdUnitResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	if r.manager == nil || !r.manager.NeedsPrivilegeEscalation() {
		return
	}
	addSudoPrivilegeWarningOnce(diags)
}

func hostSystemdUnitPlanReady(model HostSystemdUnitResourceModel) bool {
	return !model.Name.IsNull() && !model.Name.IsUnknown() &&
		!model.Content.IsNull() && !model.Content.IsUnknown() &&
		!model.Mode.IsNull() && !model.Mode.IsUnknown()
}

func systemdUnitSpecFromModel(model HostSystemdUnitResourceModel) (SystemdUnitSpec, error) {
	mode, err := parseHostDirMode(model.Mode.ValueString())
	if err != nil {
		return SystemdUnitSpec{}, err
	}
	return SystemdUnitSpec{
		Name:    model.Name.ValueString(),
		Content: model.Content.ValueString(),
		Mode:    mode,
	}, nil
}

func hostSystemdUnitPlanRequiresMutationWarning(ctx context.Context, stateData tfsdk.State, plan HostSystemdUnitResourceModel, diags *diag.Diagnostics) bool {
	if stateData.Raw.IsNull() {
		return true
	}

	var state HostSystemdUnitResourceModel
	diags.Append(stateData.Get(ctx, &state)...)
	if diags.HasError() || !hostSystemdUnitPlanReady(state) {
		return false
	}

	return plan.Name.ValueString() != state.Name.ValueString() ||
		plan.Content.ValueString() != state.Content.ValueString() ||
		plan.Mode.ValueString() != state.Mode.ValueString()
}
