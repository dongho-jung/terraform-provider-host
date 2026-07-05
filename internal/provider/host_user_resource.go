package provider

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	tfpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &HostUserResource{}
	_ resource.ResourceWithConfigure   = &HostUserResource{}
	_ resource.ResourceWithImportState = &HostUserResource{}
	_ resource.ResourceWithModifyPlan  = &HostUserResource{}
)

type HostUserResource struct {
	manager IdentityManager
}

type HostUserResourceModel struct {
	ID                  types.String `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	FullName            types.String `tfsdk:"full_name"`
	HomeDir             types.String `tfsdk:"home_dir"`
	Shell               types.String `tfsdk:"shell"`
	CreateHome          types.Bool   `tfsdk:"create_home"`
	Groups              types.Set    `tfsdk:"groups"`
	RemoveHomeOnDestroy types.Bool   `tfsdk:"remove_home_on_destroy"`
	UID                 types.String `tfsdk:"uid"`
	GID                 types.String `tfsdk:"gid"`
}

func NewHostUserResource() resource.Resource {
	return &HostUserResource{}
}

func (r *HostUserResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *HostUserResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one local host user without managing its password.",
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
				MarkdownDescription: "Local username.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"full_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "User full name/comment. Omit to leave this field unmanaged.",
			},
			"home_dir": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "User home directory. Omit to use the operating system default and leave this field unmanaged after creation.",
			},
			"shell": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "User login shell. Omit to use the operating system default and leave this field unmanaged.",
			},
			"create_home": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Create the user's home directory when creating the user. On Linux, changing `home_dir` later moves the home directory when this is true.",
			},
			"groups": schema.SetAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				Computed:            true,
				Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, nil)),
				MarkdownDescription: "Supplementary groups managed by this resource. Groups outside this set are left untouched.",
			},
			"remove_home_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Remove the user's home directory when destroying the resource. Defaults to false.",
			},
			"uid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Numeric user ID as reported by the operating system.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"gid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Numeric primary group ID as reported by the operating system.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *HostUserResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.IdentityManager == nil {
			resp.Diagnostics.AddError("Identity backend unavailable", "`host_user` requires local user command line tools.")
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

func (r *HostUserResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		var state HostUserResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() || state.Name.IsNull() || state.Name.IsUnknown() {
			return
		}
		r.addPrivilegeWarning(&resp.Diagnostics)
		return
	}

	var plan HostUserResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !hostUserPlanReady(plan) {
		return
	}

	spec, diags := hostUserSpecFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validateHostUserSpec(spec); err != nil {
		resp.Diagnostics.AddError("Invalid host user", err.Error())
		return
	}

	plan.ID = types.StringValue(spec.Username)
	requiresMutationWarning, mutationDiags := hostUserPlanRequiresMutationWarning(ctx, req.State, plan)
	resp.Diagnostics.Append(mutationDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if requiresMutationWarning {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostUserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostUserResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	spec, diags := hostUserSpecFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.UpsertUser(ctx, spec, nil); err != nil {
		resp.Diagnostics.AddError("Failed to sync host user", err.Error())
		return
	}

	state, exists, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read host user", err.Error())
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Failed to read host user", fmt.Sprintf("User %q was not found after creation.", spec.Username))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostUserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostUserResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	newState, exists, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read host user", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *HostUserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostUserResourceModel
	var state HostUserResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	spec, diags := hostUserSpecFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	previousGroups, groupDiags := hostUserGroupsFromSet(ctx, state.Groups)
	resp.Diagnostics.Append(groupDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.UpsertUser(ctx, spec, previousGroups); err != nil {
		resp.Diagnostics.AddError("Failed to sync host user", err.Error())
		return
	}

	newState, exists, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read host user", err.Error())
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Failed to read host user", fmt.Sprintf("User %q was not found after update.", spec.Username))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *HostUserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostUserResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.DeleteUser(ctx, state.Name.ValueString(), state.RemoveHomeOnDestroy.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Failed to remove host user", err.Error())
	}
}

func (r *HostUserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, tfpath.Root("name"), req, resp)
}

func (r *HostUserResource) refreshState(ctx context.Context, model HostUserResourceModel) (HostUserResourceModel, bool, error) {
	username := model.Name.ValueString()
	status, exists, err := r.manager.UserStatus(ctx, username)
	if err != nil {
		return model, false, err
	}
	if !exists {
		return model, false, nil
	}

	model.ID = types.StringValue(status.Username)
	model.Name = types.StringValue(status.Username)
	model.UID = types.StringValue(status.UID)
	model.GID = types.StringValue(status.GID)

	if !model.FullName.IsNull() {
		model.FullName = types.StringValue(status.FullName)
	}
	if !model.HomeDir.IsNull() {
		model.HomeDir = types.StringValue(status.Home)
	}
	if !model.Shell.IsNull() {
		model.Shell = types.StringValue(status.Shell)
	}
	if model.CreateHome.IsNull() || model.CreateHome.IsUnknown() {
		model.CreateHome = types.BoolValue(true)
	}
	if model.RemoveHomeOnDestroy.IsNull() || model.RemoveHomeOnDestroy.IsUnknown() {
		model.RemoveHomeOnDestroy = types.BoolValue(false)
	}

	managedGroups, diags := hostUserGroupsFromSet(ctx, model.Groups)
	if diags.HasError() {
		return model, false, fmt.Errorf("read managed groups from state")
	}
	presentManagedGroups := intersectSortedStrings(managedGroups, status.Groups)
	groupSet, diags := hostUserStringSet(ctx, presentManagedGroups)
	if diags.HasError() {
		return model, false, fmt.Errorf("store managed groups in state")
	}
	model.Groups = groupSet

	return model, true, nil
}

func (r *HostUserResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	if r.manager == nil || !r.manager.NeedsPrivilegeEscalation() {
		return
	}

	addSudoPrivilegeWarningOnce(diags)
}

func hostUserPlanRequiresMutationWarning(ctx context.Context, stateData tfsdk.State, plan HostUserResourceModel) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	if stateData.Raw.IsNull() {
		return true, diags
	}

	var state HostUserResourceModel
	diags.Append(stateData.Get(ctx, &state)...)
	if diags.HasError() {
		return false, diags
	}

	if !sameKnownString(plan.Name, state.Name) ||
		!sameKnownString(plan.FullName, state.FullName) ||
		!sameKnownString(plan.HomeDir, state.HomeDir) ||
		!sameKnownString(plan.Shell, state.Shell) ||
		!sameKnownBool(plan.CreateHome, state.CreateHome) ||
		!sameKnownBool(plan.RemoveHomeOnDestroy, state.RemoveHomeOnDestroy) {
		return true, diags
	}

	planGroups, planGroupDiags := hostUserGroupsFromSet(ctx, plan.Groups)
	diags.Append(planGroupDiags...)
	stateGroups, stateGroupDiags := hostUserGroupsFromSet(ctx, state.Groups)
	diags.Append(stateGroupDiags...)
	if diags.HasError() {
		return false, diags
	}

	return !slices.Equal(planGroups, stateGroups), diags
}

func sameKnownString(left types.String, right types.String) bool {
	if left.IsUnknown() || right.IsUnknown() {
		return false
	}
	if left.IsNull() || right.IsNull() {
		return left.IsNull() && right.IsNull()
	}
	return left.ValueString() == right.ValueString()
}

func sameKnownBool(left types.Bool, right types.Bool) bool {
	if left.IsUnknown() || right.IsUnknown() {
		return false
	}
	if left.IsNull() || right.IsNull() {
		return left.IsNull() && right.IsNull()
	}
	return left.ValueBool() == right.ValueBool()
}

func hostUserPlanReady(model HostUserResourceModel) bool {
	if model.Name.IsNull() || model.Name.IsUnknown() ||
		model.FullName.IsUnknown() || model.HomeDir.IsUnknown() || model.Shell.IsUnknown() ||
		model.CreateHome.IsNull() || model.CreateHome.IsUnknown() ||
		model.Groups.IsNull() || model.Groups.IsUnknown() ||
		model.RemoveHomeOnDestroy.IsNull() || model.RemoveHomeOnDestroy.IsUnknown() {
		return false
	}

	return true
}

func hostUserSpecFromModel(ctx context.Context, model HostUserResourceModel) (HostUserSpec, diag.Diagnostics) {
	var diags diag.Diagnostics

	groups, groupDiags := hostUserGroupsFromSet(ctx, model.Groups)
	diags.Append(groupDiags...)
	if diags.HasError() {
		return HostUserSpec{}, diags
	}

	spec := HostUserSpec{
		Username:   model.Name.ValueString(),
		CreateHome: model.CreateHome.ValueBool(),
		Groups:     groups,
	}

	if !model.FullName.IsNull() {
		value := model.FullName.ValueString()
		spec.FullName = &value
	}
	if !model.HomeDir.IsNull() {
		value := model.HomeDir.ValueString()
		spec.Home = &value
	}
	if !model.Shell.IsNull() {
		value := model.Shell.ValueString()
		spec.Shell = &value
	}

	return spec, diags
}

func hostUserGroupsFromSet(ctx context.Context, value types.Set) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if value.IsNull() {
		return nil, diags
	}
	if value.IsUnknown() {
		diags.AddError("Invalid groups", "groups set is unknown")
		return nil, diags
	}

	var elements []string
	diags.Append(value.ElementsAs(ctx, &elements, false)...)
	if diags.HasError() {
		return nil, diags
	}

	sort.Strings(elements)
	return elements, diags
}

func hostUserStringSet(ctx context.Context, values []string) (types.Set, diag.Diagnostics) {
	if values == nil {
		values = []string{}
	}
	sort.Strings(values)
	return types.SetValueFrom(ctx, types.StringType, values)
}

func intersectSortedStrings(left []string, right []string) []string {
	rightSet := stringSet(right)
	var intersection []string
	for _, value := range left {
		if _, ok := rightSet[value]; ok {
			intersection = append(intersection, value)
		}
	}
	sort.Strings(intersection)
	return intersection
}
