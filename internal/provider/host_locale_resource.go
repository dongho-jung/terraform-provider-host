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
	_ resource.Resource                = &HostLocaleResource{}
	_ resource.ResourceWithConfigure   = &HostLocaleResource{}
	_ resource.ResourceWithImportState = &HostLocaleResource{}
	_ resource.ResourceWithModifyPlan  = &HostLocaleResource{}
)

type HostLocaleResource struct {
	manager LocaleManager
}

type HostLocaleResourceModel struct {
	ID   types.String `tfsdk:"id"`
	Lang types.String `tfsdk:"lang"`
}

func NewHostLocaleResource() resource.Resource {
	return &HostLocaleResource{}
}

func (r *HostLocaleResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_locale"
}

func (r *HostLocaleResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the system locale LANG value using `localectl`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, equal to `lang`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"lang": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "System locale LANG value, such as `en_US.UTF-8`.",
			},
		},
	}
}

func (r *HostLocaleResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.LocaleManager == nil {
			resp.Diagnostics.AddError("Locale backend unavailable", "`host_locale` requires `localectl`.")
			return
		}
		r.manager = data.LocaleManager
	case LocaleManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or LocaleManager, got %T.", req.ProviderData),
		)
	}
}

func (r *HostLocaleResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostLocaleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Lang.IsNull() || plan.Lang.IsUnknown() {
		return
	}

	lang := plan.Lang.ValueString()
	if err := validateHostLocale(lang); err != nil {
		resp.Diagnostics.AddError("Invalid locale", err.Error())
		return
	}

	plan.ID = types.StringValue(lang)
	if hostLocalePlanRequiresMutationWarning(ctx, req.State, plan, &resp.Diagnostics) {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostLocaleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostLocaleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SetLocale(ctx, plan.Lang.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to set locale", err.Error())
		return
	}

	state, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read locale", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostLocaleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostLocaleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read locale", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostLocaleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostLocaleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.SetLocale(ctx, plan.Lang.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to set locale", err.Error())
		return
	}

	state, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read locale", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostLocaleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
}

func (r *HostLocaleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("lang"), req, resp)
}

func (r *HostLocaleResource) refreshState(ctx context.Context, model HostLocaleResourceModel) (HostLocaleResourceModel, error) {
	lang, err := r.manager.Locale(ctx)
	if err != nil {
		return model, err
	}
	if err := validateHostLocale(lang); err != nil {
		return model, err
	}
	model.ID = types.StringValue(lang)
	model.Lang = types.StringValue(lang)
	return model, nil
}

func (r *HostLocaleResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	if r.manager == nil || !r.manager.NeedsPrivilegeEscalation() {
		return
	}
	addSudoPrivilegeWarningOnce(diags)
}

func hostLocalePlanRequiresMutationWarning(ctx context.Context, stateData tfsdk.State, plan HostLocaleResourceModel, diags *diag.Diagnostics) bool {
	if stateData.Raw.IsNull() {
		return true
	}

	var state HostLocaleResourceModel
	diags.Append(stateData.Get(ctx, &state)...)
	if diags.HasError() || state.Lang.IsNull() || state.Lang.IsUnknown() {
		return false
	}

	return plan.Lang.ValueString() != state.Lang.ValueString()
}
