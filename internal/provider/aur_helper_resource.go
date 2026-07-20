package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &AURHelperResource{}
	_ resource.ResourceWithConfigure   = &AURHelperResource{}
	_ resource.ResourceWithImportState = &AURHelperResource{}
	_ resource.ResourceWithModifyPlan  = &AURHelperResource{}
)

type AURHelperResource struct {
	manager AURHelperManager
}

type AURHelperResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Package          types.String `tfsdk:"package"`
	Path             types.String `tfsdk:"path"`
	InstalledVersion types.String `tfsdk:"installed_version"`
	InstallReason    types.String `tfsdk:"install_reason"`
	DeleteOnDestroy  types.Bool   `tfsdk:"delete_on_destroy"`
}

func NewAURHelperResource() resource.Resource {
	return &AURHelperResource{}
}

func (r *AURHelperResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_aur_helper"
}

func (r *AURHelperResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Bootstraps and manages an AUR helper package without requiring an existing AUR helper.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, equal to `name`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "AUR helper executable name. Supported values are `yay` and `paru`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"package": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "AUR package that provides the helper executable. Defaults to `name`; variants such as `yay-bin` are supported.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"delete_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Remove the helper package when this resource is destroyed. Defaults to false so provider bootstrap tooling is retained.",
			},
			"install_reason": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(packageInstallReasonExplicit),
				MarkdownDescription: "Desired and observed Pacman install reason. The only supported desired value is `explicit`; refresh records `dependency` after external drift so the next apply restores `explicit`.",
			},
			"path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved helper executable path.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"installed_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Installed helper package version.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *AURHelperResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.AURHelperManager == nil {
			resp.Diagnostics.AddError("AUR helper bootstrap unavailable", "`host_aur_helper` requires Pacman on an Arch Linux host.")
			return
		}
		r.manager = data.AURHelperManager
	case AURHelperManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected HostProviderData or AURHelperManager, got %T.", req.ProviderData))
	}
}

func (r *AURHelperResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan AURHelperResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Name.IsNull() || plan.Name.IsUnknown() {
		return
	}
	if err := validateAURHelperName(plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid AUR helper", err.Error())
		return
	}
	var config AURHelperResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.Package.IsNull() {
		plan.Package = plan.Name
	} else if config.Package.IsUnknown() || plan.Package.IsUnknown() {
		plan.ID = plan.Name
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
		return
	}
	spec := aurHelperSpecFromModel(plan)
	if err := validateAURHelperSpec(spec); err != nil {
		resp.Diagnostics.AddError("Invalid AUR helper", err.Error())
		return
	}
	if !plan.InstallReason.IsUnknown() {
		if err := validateInstallReasonPolicy(plan.InstallReason.ValueString()); err != nil {
			resp.Diagnostics.AddError("Invalid AUR helper install reason", err.Error())
			return
		}
	}
	plan.ID = plan.Name
	if req.State.Raw.IsNull() && r.manager != nil && r.manager.NeedsPrivilegeEscalation() {
		addSudoPrivilegeWarningOnce(&resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *AURHelperResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan AURHelperResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateInstallReasonPolicy(plan.InstallReason.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid AUR helper install reason", err.Error())
		return
	}
	status, err := r.manager.EnsureHelper(ctx, aurHelperSpecFromModel(plan))
	if err != nil {
		resp.Diagnostics.AddError("Failed to bootstrap AUR helper", err.Error())
		return
	}
	hydrateAURHelperState(&plan, status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AURHelperResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AURHelperResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	status, exists, err := r.manager.HelperStatus(ctx, aurHelperSpecFromModel(state))
	if err != nil {
		resp.Diagnostics.AddError("Failed to read AUR helper", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}
	hydrateAURHelperState(&state, status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AURHelperResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan AURHelperResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateInstallReasonPolicy(plan.InstallReason.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid AUR helper install reason", err.Error())
		return
	}
	status, err := r.manager.EnsureHelper(ctx, aurHelperSpecFromModel(plan))
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync AUR helper", err.Error())
		return
	}
	hydrateAURHelperState(&plan, status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AURHelperResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state AURHelperResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !state.DeleteOnDestroy.ValueBool() {
		return
	}
	if err := r.manager.RemoveHelper(ctx, aurHelperSpecFromModel(state)); err != nil {
		resp.Diagnostics.AddError("Failed to remove AUR helper", err.Error())
	}
}

func (r *AURHelperResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, ":")
	if len(parts) > 2 || parts[0] == "" {
		resp.Diagnostics.AddError("Invalid AUR helper import ID", "Use `<name>` or `<name>:<package>`.")
		return
	}
	name := parts[0]
	packageName := name
	if len(parts) == 2 {
		packageName = parts[1]
	}
	if err := validateAURHelperSpec(AURHelperSpec{Name: name, Package: packageName}); err != nil {
		resp.Diagnostics.AddError("Invalid AUR helper import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("package"), packageName)...)
}

func aurHelperSpecFromModel(model AURHelperResourceModel) AURHelperSpec {
	return AURHelperSpec{Name: model.Name.ValueString(), Package: model.Package.ValueString()}
}

func hydrateAURHelperState(model *AURHelperResourceModel, status AURHelperStatus) {
	model.ID = types.StringValue(status.Name)
	model.Name = types.StringValue(status.Name)
	model.Package = types.StringValue(status.Package)
	model.Path = types.StringValue(status.Path)
	model.InstalledVersion = types.StringValue(status.InstalledVersion)
	hydratePackageInstallReason(&model.InstallReason, PackageStatus{Installed: true, ReasonUser: status.ReasonUser})
	if model.DeleteOnDestroy.IsNull() || model.DeleteOnDestroy.IsUnknown() {
		model.DeleteOnDestroy = types.BoolValue(false)
	}
}
