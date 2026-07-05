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
	_ resource.Resource                = &HostTimezoneResource{}
	_ resource.ResourceWithConfigure   = &HostTimezoneResource{}
	_ resource.ResourceWithImportState = &HostTimezoneResource{}
	_ resource.ResourceWithModifyPlan  = &HostTimezoneResource{}
)

type HostTimezoneResource struct {
	manager TimezoneManager
}

type HostTimezoneResourceModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func NewHostTimezoneResource() resource.Resource {
	return &HostTimezoneResource{}
}

func (r *HostTimezoneResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_timezone"
}

func (r *HostTimezoneResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the system timezone.",
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
				MarkdownDescription: "IANA timezone name, such as `America/Los_Angeles` or `UTC`.",
			},
		},
	}
}

func (r *HostTimezoneResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.TimezoneManager == nil {
			resp.Diagnostics.AddError("Timezone backend unavailable", "`host_timezone` requires `timedatectl` on Linux or `systemsetup` on macOS.")
			return
		}
		r.manager = data.TimezoneManager
	case TimezoneManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or TimezoneManager, got %T.", req.ProviderData),
		)
	}
}

func (r *HostTimezoneResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostTimezoneResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Name.IsNull() || plan.Name.IsUnknown() {
		return
	}

	name := plan.Name.ValueString()
	if err := validateHostTimezone(name); err != nil {
		resp.Diagnostics.AddError("Invalid timezone", err.Error())
		return
	}

	plan.ID = types.StringValue(name)
	if hostTimezonePlanRequiresMutationWarning(ctx, req.State, plan, &resp.Diagnostics) {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostTimezoneResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostTimezoneResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SetTimezone(ctx, plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to set timezone", err.Error())
		return
	}

	state, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read timezone", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostTimezoneResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostTimezoneResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read timezone", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostTimezoneResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostTimezoneResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SetTimezone(ctx, plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to set timezone", err.Error())
		return
	}

	state, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read timezone", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostTimezoneResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
}

func (r *HostTimezoneResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *HostTimezoneResource) refreshState(ctx context.Context, model HostTimezoneResourceModel) (HostTimezoneResourceModel, error) {
	name, err := r.manager.Timezone(ctx)
	if err != nil {
		return model, err
	}
	if err := validateHostTimezone(name); err != nil {
		return model, err
	}
	model.ID = types.StringValue(name)
	model.Name = types.StringValue(name)
	return model, nil
}

func (r *HostTimezoneResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	if r.manager == nil || !r.manager.NeedsPrivilegeEscalation() {
		return
	}

	addSudoPrivilegeWarningOnce(diags)
}

func hostTimezonePlanRequiresMutationWarning(ctx context.Context, stateData tfsdk.State, plan HostTimezoneResourceModel, diags *diag.Diagnostics) bool {
	if stateData.Raw.IsNull() {
		return true
	}

	var state HostTimezoneResourceModel
	diags.Append(stateData.Get(ctx, &state)...)
	if diags.HasError() || state.Name.IsNull() || state.Name.IsUnknown() {
		return false
	}

	return plan.Name.ValueString() != state.Name.ValueString()
}
