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
	_ resource.Resource                = &HostHostnameResource{}
	_ resource.ResourceWithConfigure   = &HostHostnameResource{}
	_ resource.ResourceWithImportState = &HostHostnameResource{}
	_ resource.ResourceWithModifyPlan  = &HostHostnameResource{}
)

type HostHostnameResource struct {
	manager HostnameManager
}

type HostHostnameResourceModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func NewHostHostnameResource() resource.Resource {
	return &HostHostnameResource{}
}

func (r *HostHostnameResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_hostname"
}

func (r *HostHostnameResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the system hostname.",
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
				MarkdownDescription: "System hostname.",
			},
		},
	}
}

func (r *HostHostnameResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.HostnameManager == nil {
			resp.Diagnostics.AddError("Hostname backend unavailable", "`host_hostname` requires `hostnamectl` on Linux or `scutil` on macOS.")
			return
		}
		r.manager = data.HostnameManager
	case HostnameManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or HostnameManager, got %T.", req.ProviderData),
		)
	}
}

func (r *HostHostnameResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostHostnameResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Name.IsNull() || plan.Name.IsUnknown() {
		return
	}

	name := plan.Name.ValueString()
	if err := validateHostHostname(name); err != nil {
		resp.Diagnostics.AddError("Invalid hostname", err.Error())
		return
	}

	plan.ID = types.StringValue(name)
	if hostHostnamePlanRequiresMutationWarning(ctx, req.State, plan, &resp.Diagnostics) {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostHostnameResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostHostnameResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SetHostname(ctx, plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to set hostname", err.Error())
		return
	}

	state, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read hostname", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostHostnameResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostHostnameResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read hostname", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostHostnameResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostHostnameResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SetHostname(ctx, plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to set hostname", err.Error())
		return
	}

	state, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read hostname", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostHostnameResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
}

func (r *HostHostnameResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *HostHostnameResource) refreshState(ctx context.Context, model HostHostnameResourceModel) (HostHostnameResourceModel, error) {
	name, err := r.manager.Hostname(ctx)
	if err != nil {
		return model, err
	}
	if err := validateHostHostname(name); err != nil {
		return model, err
	}
	model.ID = types.StringValue(name)
	model.Name = types.StringValue(name)
	return model, nil
}

func (r *HostHostnameResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	if r.manager == nil || !r.manager.NeedsPrivilegeEscalation() {
		return
	}

	addSudoPrivilegeWarningOnce(diags)
}

func hostHostnamePlanRequiresMutationWarning(ctx context.Context, stateData tfsdk.State, plan HostHostnameResourceModel, diags *diag.Diagnostics) bool {
	if stateData.Raw.IsNull() {
		return true
	}

	var state HostHostnameResourceModel
	diags.Append(stateData.Get(ctx, &state)...)
	if diags.HasError() || state.Name.IsNull() || state.Name.IsUnknown() {
		return false
	}

	return plan.Name.ValueString() != state.Name.ValueString()
}
