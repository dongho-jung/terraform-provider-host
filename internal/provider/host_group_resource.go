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
	_ resource.Resource                = &HostGroupResource{}
	_ resource.ResourceWithConfigure   = &HostGroupResource{}
	_ resource.ResourceWithImportState = &HostGroupResource{}
	_ resource.ResourceWithModifyPlan  = &HostGroupResource{}
)

type HostGroupResource struct {
	manager IdentityManager
}

type HostGroupResourceModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
	GID  types.String `tfsdk:"gid"`
}

func NewHostGroupResource() resource.Resource {
	return &HostGroupResource{}
}

func (r *HostGroupResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *HostGroupResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a local host group.",
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
				MarkdownDescription: "Local group name.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"gid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Numeric group ID as reported by the operating system.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *HostGroupResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.IdentityManager == nil {
			resp.Diagnostics.AddError("Identity backend unavailable", "`host_group` requires local user/group command line tools.")
			return
		}
		r.manager = data.IdentityManager
	case IdentityManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or IdentityManager, got %T.", req.ProviderData),
		)
	}
}

func (r *HostGroupResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		var state HostGroupResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() || state.Name.IsNull() || state.Name.IsUnknown() {
			return
		}
		r.addPrivilegeWarning(&resp.Diagnostics, "remove", state.Name.ValueString())
		return
	}

	var plan HostGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Name.IsNull() || plan.Name.IsUnknown() {
		return
	}

	name := plan.Name.ValueString()
	if err := validateHostGroupName(name); err != nil {
		resp.Diagnostics.AddError("Invalid host group", err.Error())
		return
	}

	plan.ID = types.StringValue(name)
	if hostGroupPlanRequiresMutationWarning(ctx, req.State, plan, &resp.Diagnostics) {
		r.addPrivilegeWarning(&resp.Diagnostics, "manage", name)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.EnsureGroup(ctx, plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to sync host group", err.Error())
		return
	}

	state, exists, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read host group", err.Error())
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Failed to read host group", fmt.Sprintf("Group %q was not found after creation.", plan.Name.ValueString()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	newState, exists, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read host group", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *HostGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, exists, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read host group", err.Error())
		return
	}
	if !exists {
		if err := r.manager.EnsureGroup(ctx, plan.Name.ValueString()); err != nil {
			resp.Diagnostics.AddError("Failed to sync host group", err.Error())
			return
		}
		state, _, err = r.refreshState(ctx, plan)
		if err != nil {
			resp.Diagnostics.AddError("Failed to read host group", err.Error())
			return
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.DeleteGroup(ctx, state.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to remove host group", err.Error())
	}
}

func (r *HostGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *HostGroupResource) refreshState(ctx context.Context, model HostGroupResourceModel) (HostGroupResourceModel, bool, error) {
	name := model.Name.ValueString()
	status, exists, err := r.manager.GroupStatus(ctx, name)
	if err != nil {
		return model, false, err
	}
	if !exists {
		return model, false, nil
	}

	model.ID = types.StringValue(status.Name)
	model.Name = types.StringValue(status.Name)
	model.GID = types.StringValue(status.GID)
	return model, true, nil
}

func (r *HostGroupResource) addPrivilegeWarning(diags *diag.Diagnostics, action string, name string) {
	if r.manager == nil || !r.manager.NeedsPrivilegeEscalation() {
		return
	}

	addSudoPrivilegeWarningOnce(diags)
}

func hostGroupPlanRequiresMutationWarning(ctx context.Context, stateData tfsdk.State, plan HostGroupResourceModel, diags *diag.Diagnostics) bool {
	if stateData.Raw.IsNull() {
		return true
	}

	var state HostGroupResourceModel
	diags.Append(stateData.Get(ctx, &state)...)
	if diags.HasError() || state.Name.IsNull() || state.Name.IsUnknown() {
		return false
	}

	return plan.Name.ValueString() != state.Name.ValueString()
}
