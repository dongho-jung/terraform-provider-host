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
	_ resource.Resource                = &PacmanPackageResource{}
	_ resource.ResourceWithConfigure   = &PacmanPackageResource{}
	_ resource.ResourceWithImportState = &PacmanPackageResource{}
	_ resource.ResourceWithModifyPlan  = &PacmanPackageResource{}
)

type PacmanPackageResource struct {
	manager PackageManager
}

type PacmanPackageResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Version          types.String `tfsdk:"version"`
	IgnoreVersion    types.Bool   `tfsdk:"ignore_version"`
	Autoremove       types.Bool   `tfsdk:"autoremove"`
	InstalledVersion types.String `tfsdk:"installed_version"`
	CandidateVersion types.String `tfsdk:"candidate_version"`
}

func NewPacmanPackageResource() resource.Resource {
	return &PacmanPackageResource{}
}

func (r *PacmanPackageResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_package_pacman"
}

func (r *PacmanPackageResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a single Pacman package and keeps its install reason marked as explicit.",
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
				MarkdownDescription: "Pacman package name.",
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
				MarkdownDescription: "Ignore available version updates. When true, the resource manages package presence and explicit install reason without planning upgrades for new candidate versions.",
			},
			"autoremove": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Remove unused dependencies recursively with `pacman -Rns` when this package is removed. Defaults to false.",
			},
			"installed_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Installed Pacman package version.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"candidate_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Latest Pacman package version from sync databases. Null when `ignore_version` is true.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *PacmanPackageResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.PacmanManager == nil {
			resp.Diagnostics.AddError(
				"Pacman executable not found",
				"`host_package_pacman` requires `pacman` to be available in PATH.",
			)
			return
		}
		r.manager = data.PacmanManager
	case PackageManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or PackageManager, got %T.", req.ProviderData),
		)
	}
}

func (r *PacmanPackageResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if r.manager == nil {
		return
	}

	if req.Plan.Raw.IsNull() {
		var state PacmanPackageResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() || state.Name.IsNull() || state.Name.IsUnknown() {
			return
		}

		r.addPrivilegeWarning(&resp.Diagnostics)
		return
	}

	var plan PacmanPackageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validateVersionPolicy(plan.Version.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid Pacman package version policy", err.Error())
		return
	}

	if plan.Name.IsNull() || plan.Name.IsUnknown() {
		return
	}

	status, err := r.manager.PackageStatus(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read Pacman package", err.Error())
		return
	}

	hydratePacmanVersionState(&plan, status)
	if pacmanPackageIgnoresVersion(plan) {
		plan.CandidateVersion = types.StringNull()
	}
	if !status.Installed {
		plan.InstalledVersion = types.StringUnknown()
		r.addPrivilegeWarning(&resp.Diagnostics)
	} else if !pacmanPackageIgnoresVersion(plan) && shouldUpgradeToLatest(plan.Version.ValueString(), status) {
		plan.InstalledVersion = types.StringValue(status.UpgradeVersion)
		r.addPrivilegeWarning(&resp.Diagnostics)
	} else if !status.ReasonUser {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *PacmanPackageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PacmanPackageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := plan.Name.ValueString()
	if err := validatePackageName(name); err != nil {
		resp.Diagnostics.AddError("Invalid Pacman package name", err.Error())
		return
	}

	if err := validateVersionPolicy(plan.Version.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid Pacman package version policy", err.Error())
		return
	}

	if err := r.syncPackage(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Failed to sync Pacman package", err.Error())
		return
	}

	state, _, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read Pacman package", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PacmanPackageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PacmanPackageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	newState, installed, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read Pacman package", err.Error())
		return
	}

	if !installed {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *PacmanPackageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PacmanPackageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validateVersionPolicy(plan.Version.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid Pacman package version policy", err.Error())
		return
	}

	if err := r.syncPackage(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Failed to sync Pacman package", err.Error())
		return
	}

	state, _, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read Pacman package", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PacmanPackageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PacmanPackageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	if err := r.manager.RemovePackages(ctx, []string{name}, state.Autoremove.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Failed to remove Pacman package", err.Error())
		return
	}
}

func (r *PacmanPackageResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	reporter, ok := r.manager.(privilegeEscalationReporter)
	if !ok || !reporter.NeedsPrivilegeEscalation() {
		return
	}

	addSudoPrivilegeWarningOnce(diags)
}

func (r *PacmanPackageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *PacmanPackageResource) refreshState(ctx context.Context, model PacmanPackageResourceModel) (PacmanPackageResourceModel, bool, error) {
	name := model.Name.ValueString()

	status, err := r.manager.PackageStatus(ctx, name)
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
	hydratePacmanVersionState(&model, status)
	if pacmanPackageIgnoresVersion(model) {
		model.CandidateVersion = types.StringNull()
	}

	return model, status.Installed, nil
}

func (r *PacmanPackageResource) syncPackage(ctx context.Context, model PacmanPackageResourceModel) error {
	name := model.Name.ValueString()

	status, err := r.manager.PackageStatus(ctx, name)
	if err != nil {
		return err
	}

	if !status.Installed {
		if err := r.manager.InstallPackages(ctx, []string{name}); err != nil {
			return err
		}
	} else if !pacmanPackageIgnoresVersion(model) && shouldUpgradeToLatest(model.Version.ValueString(), status) {
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

func pacmanPackageIgnoresVersion(model PacmanPackageResourceModel) bool {
	return model.IgnoreVersion.IsNull() || model.IgnoreVersion.IsUnknown() || model.IgnoreVersion.ValueBool()
}

func hydratePacmanVersionState(model *PacmanPackageResourceModel, status PackageStatus) {
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
