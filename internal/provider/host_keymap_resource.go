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
	_ resource.Resource                = &HostKeymapResource{}
	_ resource.ResourceWithConfigure   = &HostKeymapResource{}
	_ resource.ResourceWithImportState = &HostKeymapResource{}
	_ resource.ResourceWithModifyPlan  = &HostKeymapResource{}
)

type HostKeymapResource struct {
	manager KeymapManager
}

type HostKeymapResourceModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func NewHostKeymapResource() resource.Resource {
	return &HostKeymapResource{}
}

func (r *HostKeymapResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_keymap"
}

func (r *HostKeymapResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the system virtual console keymap using `localectl`.",
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
				MarkdownDescription: "Virtual console keymap name, such as `us` or `jp106`.",
			},
		},
	}
}

func (r *HostKeymapResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.KeymapManager == nil {
			resp.Diagnostics.AddError("Keymap backend unavailable", "`host_keymap` requires `localectl`.")
			return
		}
		r.manager = data.KeymapManager
	case KeymapManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or KeymapManager, got %T.", req.ProviderData),
		)
	}
}

func (r *HostKeymapResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostKeymapResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Name.IsNull() || plan.Name.IsUnknown() {
		return
	}

	name := plan.Name.ValueString()
	if err := validateHostKeymap(name); err != nil {
		resp.Diagnostics.AddError("Invalid keymap", err.Error())
		return
	}

	plan.ID = types.StringValue(name)
	if hostKeymapPlanRequiresMutationWarning(ctx, req.State, plan, &resp.Diagnostics) {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostKeymapResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostKeymapResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SetKeymap(ctx, plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to set keymap", err.Error())
		return
	}

	state, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read keymap", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostKeymapResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostKeymapResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read keymap", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostKeymapResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostKeymapResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SetKeymap(ctx, plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to set keymap", err.Error())
		return
	}

	state, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read keymap", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostKeymapResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
}

func (r *HostKeymapResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *HostKeymapResource) refreshState(ctx context.Context, model HostKeymapResourceModel) (HostKeymapResourceModel, error) {
	name, err := r.manager.Keymap(ctx)
	if err != nil {
		return model, err
	}
	if err := validateHostKeymap(name); err != nil {
		return model, err
	}
	model.ID = types.StringValue(name)
	model.Name = types.StringValue(name)
	return model, nil
}

func (r *HostKeymapResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	if r.manager == nil || !r.manager.NeedsPrivilegeEscalation() {
		return
	}
	addSudoPrivilegeWarningOnce(diags)
}

func hostKeymapPlanRequiresMutationWarning(ctx context.Context, stateData tfsdk.State, plan HostKeymapResourceModel, diags *diag.Diagnostics) bool {
	if stateData.Raw.IsNull() {
		return true
	}

	var state HostKeymapResourceModel
	diags.Append(stateData.Get(ctx, &state)...)
	if diags.HasError() || state.Name.IsNull() || state.Name.IsUnknown() {
		return false
	}

	return plan.Name.ValueString() != state.Name.ValueString()
}
