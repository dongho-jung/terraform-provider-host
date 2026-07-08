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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &AURPackageResource{}
	_ resource.ResourceWithConfigure   = &AURPackageResource{}
	_ resource.ResourceWithImportState = &AURPackageResource{}
	_ resource.ResourceWithModifyPlan  = &AURPackageResource{}
)

type AURPackageResource struct {
	manager AURPackageManager
}

type AURPackageResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Version          types.String `tfsdk:"version"`
	IgnoreVersion    types.Bool   `tfsdk:"ignore_version"`
	Autoremove       types.Bool   `tfsdk:"autoremove"`
	InstalledVersion types.String `tfsdk:"installed_version"`
	CandidateVersion types.String `tfsdk:"candidate_version"`
}

func NewAURPackageResource() resource.Resource {
	return &AURPackageResource{}
}

func (r *AURPackageResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_package_aur"
}

func (r *AURPackageResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a single AUR package through an AUR helper (`yay` or `paru`) and keeps its install reason marked as explicit. The helper runs as the invoking user and escalates through its own sudo calls.",
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
				MarkdownDescription: "AUR package name.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"version": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(versionLatest),
				MarkdownDescription: "Package version policy. Only `latest` is currently supported. Used for upgrade planning only when `ignore_version` is false.",
			},
			"ignore_version": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Ignore available version updates. When true, the resource manages package presence and explicit install reason without planning upgrades, and skips AUR network lookups during refresh. Defaults to true.",
			},
			"autoremove": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Remove unused dependencies recursively with `pacman -Rns` when this package is removed. Defaults to false.",
			},
			"installed_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Installed package version from the local pacman database.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"candidate_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Latest package version reported by the AUR helper. Null when `ignore_version` is true.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *AURPackageResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.AURManager == nil {
			resp.Diagnostics.AddError(
				"AUR helper not found",
				"`host_package_aur` requires `pacman` plus an AUR helper (`yay` or `paru`) in PATH.",
			)
			return
		}
		r.manager = data.AURManager
	case AURPackageManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or AURPackageManager, got %T.", req.ProviderData),
		)
	}
}

func (r *AURPackageResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if r.manager == nil {
		return
	}

	if req.Plan.Raw.IsNull() {
		var state AURPackageResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() || state.Name.IsNull() || state.Name.IsUnknown() {
			return
		}

		r.addPrivilegeWarning(&resp.Diagnostics)
		return
	}

	var plan AURPackageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validateVersionPolicy(plan.Version.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid AUR package version policy", err.Error())
		return
	}

	if plan.Name.IsNull() || plan.Name.IsUnknown() {
		return
	}

	status, err := r.manager.PackageStatus(ctx, plan.Name.ValueString(), !aurPackageIgnoresVersion(plan))
	if err != nil {
		resp.Diagnostics.AddError("Failed to read AUR package", err.Error())
		return
	}

	hydrateAURPackageVersionState(&plan, status)
	if aurPackageIgnoresVersion(plan) {
		plan.CandidateVersion = types.StringNull()
	}
	if !status.Installed {
		plan.InstalledVersion = types.StringUnknown()
		r.addPrivilegeWarning(&resp.Diagnostics)
	} else if !aurPackageIgnoresVersion(plan) && shouldUpgradeToLatest(plan.Version.ValueString(), status) {
		plan.InstalledVersion = types.StringValue(status.UpgradeVersion)
		r.addPrivilegeWarning(&resp.Diagnostics)
	} else if !status.ReasonUser {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *AURPackageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan AURPackageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := plan.Name.ValueString()
	if err := validatePackageName(name); err != nil {
		resp.Diagnostics.AddError("Invalid AUR package name", err.Error())
		return
	}

	if err := validateVersionPolicy(plan.Version.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid AUR package version policy", err.Error())
		return
	}

	if err := r.syncPackage(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Failed to sync AUR package", err.Error())
		return
	}

	state, _, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read AUR package", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AURPackageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AURPackageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	newState, installed, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read AUR package", err.Error())
		return
	}

	if !installed {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *AURPackageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan AURPackageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validateVersionPolicy(plan.Version.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid AUR package version policy", err.Error())
		return
	}

	if err := r.syncPackage(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Failed to sync AUR package", err.Error())
		return
	}

	state, _, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read AUR package", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AURPackageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state AURPackageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	if err := r.manager.RemovePackages(ctx, []string{name}, state.Autoremove.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Failed to remove AUR package", err.Error())
		return
	}
}

func (r *AURPackageResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	reporter, ok := r.manager.(privilegeEscalationReporter)
	if !ok || !reporter.NeedsPrivilegeEscalation() {
		return
	}

	addSudoPrivilegeWarningOnce(diags)
}

func (r *AURPackageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *AURPackageResource) refreshState(ctx context.Context, model AURPackageResourceModel) (AURPackageResourceModel, bool, error) {
	name := model.Name.ValueString()

	status, err := r.manager.PackageStatus(ctx, name, !aurPackageIgnoresVersion(model))
	if err != nil {
		return model, false, err
	}

	model.ID = types.StringValue(name)
	if model.Version.IsNull() || model.Version.IsUnknown() {
		model.Version = types.StringValue(versionLatest)
	}
	if model.IgnoreVersion.IsNull() || model.IgnoreVersion.IsUnknown() {
		model.IgnoreVersion = types.BoolValue(true)
	}
	if model.Autoremove.IsNull() || model.Autoremove.IsUnknown() {
		model.Autoremove = types.BoolValue(false)
	}
	hydrateAURPackageVersionState(&model, status)
	if aurPackageIgnoresVersion(model) {
		model.CandidateVersion = types.StringNull()
	}

	return model, status.Installed, nil
}

func (r *AURPackageResource) syncPackage(ctx context.Context, model AURPackageResourceModel) error {
	name := model.Name.ValueString()

	status, err := r.manager.PackageStatus(ctx, name, !aurPackageIgnoresVersion(model))
	if err != nil {
		return err
	}

	if !status.Installed {
		if err := r.manager.InstallPackages(ctx, []string{name}); err != nil {
			return err
		}
	} else if !aurPackageIgnoresVersion(model) && shouldUpgradeToLatest(model.Version.ValueString(), status) {
		if err := r.manager.UpgradePackages(ctx, []string{name}); err != nil {
			return err
		}
	}

	if !status.ReasonUser {
		if err := r.manager.MarkUserPackages(ctx, []string{name}); err != nil {
			return err
		}
	}

	return nil
}

func aurPackageIgnoresVersion(model AURPackageResourceModel) bool {
	return model.IgnoreVersion.IsNull() || model.IgnoreVersion.IsUnknown() || model.IgnoreVersion.ValueBool()
}

func hydrateAURPackageVersionState(model *AURPackageResourceModel, status PackageStatus) {
	if status.InstalledVersion == "" {
		model.InstalledVersion = types.StringNull()
	} else {
		model.InstalledVersion = types.StringValue(status.InstalledVersion)
	}

	if status.CandidateVersion == "" {
		model.CandidateVersion = types.StringNull()
	} else {
		model.CandidateVersion = types.StringValue(status.CandidateVersion)
	}
}
