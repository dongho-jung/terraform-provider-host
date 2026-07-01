package provider

import (
	"context"
	"fmt"
	"strings"

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

const versionLatest = "latest"

var (
	_ resource.Resource                 = &DNFPackageResource{}
	_ resource.ResourceWithConfigure    = &DNFPackageResource{}
	_ resource.ResourceWithImportState  = &DNFPackageResource{}
	_ resource.ResourceWithModifyPlan   = &DNFPackageResource{}
	_ resource.ResourceWithUpgradeState = &DNFPackageResource{}
)

type DNFPackageResource struct {
	manager PackageManager
}

type privilegeEscalationReporter interface {
	NeedsPrivilegeEscalation() bool
}

type DNFPackageResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Version          types.String `tfsdk:"version"`
	Autoremove       types.Bool   `tfsdk:"autoremove"`
	InstalledVersion types.String `tfsdk:"installed_version"`
	CandidateVersion types.String `tfsdk:"candidate_version"`
}

type dnfPackageResourceModelV1 struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Autoremove types.Bool   `tfsdk:"autoremove"`
}

type dnfPackageResourceModelV0 struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Autoremove types.Bool   `tfsdk:"autoremove"`
	Installed  types.Bool   `tfsdk:"installed"`
	Reason     types.String `tfsdk:"reason"`
	ReasonUser types.Bool   `tfsdk:"reason_user"`
}

func NewDNFPackageResource() resource.Resource {
	return &DNFPackageResource{}
}

func (r *DNFPackageResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_package_dnf"
}

func (r *DNFPackageResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             2,
		MarkdownDescription: "Manages a single DNF package and keeps its DNF install reason marked as `User`.",
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
				MarkdownDescription: "DNF package name.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"version": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(versionLatest),
				MarkdownDescription: "Package version policy. Only `latest` is currently supported.",
			},
			"autoremove": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Allow DNF to remove dependencies that become unused when this package is removed.",
			},
			"installed_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "DNF EVR of the installed package.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"candidate_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "DNF EVR of the latest package candidate from enabled repositories.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *DNFPackageResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: &schema.Schema{
				Attributes: map[string]schema.Attribute{
					"id": schema.StringAttribute{
						Computed: true,
					},
					"name": schema.StringAttribute{
						Required: true,
					},
					"autoremove": schema.BoolAttribute{
						Optional: true,
						Computed: true,
					},
					"installed": schema.BoolAttribute{
						Computed: true,
					},
					"reason": schema.StringAttribute{
						Computed: true,
					},
					"reason_user": schema.BoolAttribute{
						Computed: true,
					},
				},
			},
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior dnfPackageResourceModelV0
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}

				upgraded := DNFPackageResourceModel{
					ID:               prior.ID,
					Name:             prior.Name,
					Version:          types.StringValue(versionLatest),
					Autoremove:       prior.Autoremove,
					InstalledVersion: types.StringNull(),
					CandidateVersion: types.StringNull(),
				}
				if upgraded.Autoremove.IsNull() || upgraded.Autoremove.IsUnknown() {
					upgraded.Autoremove = types.BoolValue(true)
				}

				resp.Diagnostics.Append(resp.State.Set(ctx, &upgraded)...)
			},
		},
		1: {
			PriorSchema: &schema.Schema{
				Attributes: map[string]schema.Attribute{
					"id": schema.StringAttribute{
						Computed: true,
					},
					"name": schema.StringAttribute{
						Required: true,
					},
					"autoremove": schema.BoolAttribute{
						Optional: true,
						Computed: true,
					},
				},
			},
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior dnfPackageResourceModelV1
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}

				upgraded := DNFPackageResourceModel{
					ID:               prior.ID,
					Name:             prior.Name,
					Version:          types.StringValue(versionLatest),
					Autoremove:       prior.Autoremove,
					InstalledVersion: types.StringNull(),
					CandidateVersion: types.StringNull(),
				}
				if upgraded.Autoremove.IsNull() || upgraded.Autoremove.IsUnknown() {
					upgraded.Autoremove = types.BoolValue(true)
				}

				resp.Diagnostics.Append(resp.State.Set(ctx, &upgraded)...)
			},
		},
	}
}

func (r *DNFPackageResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.PackageManager == nil {
			resp.Diagnostics.AddError(
				"DNF executable not found",
				"`host_package_dnf` requires `dnf` to be available in PATH.",
			)
			return
		}
		r.manager = data.PackageManager
	case PackageManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or PackageManager, got %T.", req.ProviderData),
		)
	}
}

func (r *DNFPackageResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if r.manager == nil {
		return
	}

	if req.Plan.Raw.IsNull() {
		var state DNFPackageResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() || state.Name.IsNull() || state.Name.IsUnknown() {
			return
		}

		r.addPrivilegeWarning(&resp.Diagnostics, "remove", state.Name.ValueString())
		return
	}

	var plan DNFPackageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validateVersionPolicy(plan.Version.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid DNF package version policy", err.Error())
		return
	}

	if plan.Name.IsNull() || plan.Name.IsUnknown() {
		return
	}

	status, err := r.manager.PackageStatus(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read DNF package", err.Error())
		return
	}

	hydrateVersionState(&plan, status)
	if !status.Installed {
		plan.InstalledVersion = types.StringUnknown()
		r.addPrivilegeWarning(&resp.Diagnostics, "install", plan.Name.ValueString())
	} else if shouldUpgradeToLatest(plan.Version.ValueString(), status) {
		plan.InstalledVersion = types.StringValue(status.UpgradeVersion)
		r.addPrivilegeWarning(&resp.Diagnostics, "upgrade", plan.Name.ValueString())
	} else if !status.ReasonUser {
		r.addPrivilegeWarning(&resp.Diagnostics, "mark as user-installed", plan.Name.ValueString())
	}

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *DNFPackageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan DNFPackageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := plan.Name.ValueString()
	if err := validatePackageName(name); err != nil {
		resp.Diagnostics.AddError("Invalid DNF package name", err.Error())
		return
	}

	if err := validateVersionPolicy(plan.Version.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid DNF package version policy", err.Error())
		return
	}

	if err := r.syncPackage(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Failed to sync DNF package", err.Error())
		return
	}

	state, _, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read DNF package", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DNFPackageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state DNFPackageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	newState, installed, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read DNF package", err.Error())
		return
	}

	if !installed {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *DNFPackageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan DNFPackageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validateVersionPolicy(plan.Version.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid DNF package version policy", err.Error())
		return
	}

	if err := r.syncPackage(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Failed to sync DNF package", err.Error())
		return
	}

	state, _, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read DNF package", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DNFPackageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state DNFPackageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	if err := r.manager.RemovePackages(ctx, []string{name}, state.Autoremove.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Failed to remove DNF package", err.Error())
		return
	}
}

func (r *DNFPackageResource) addPrivilegeWarning(diags *diag.Diagnostics, action string, name string) {
	reporter, ok := r.manager.(privilegeEscalationReporter)
	if !ok || !reporter.NeedsPrivilegeEscalation() {
		return
	}

	addSudoPrivilegeWarningOnce(diags)
}

func (r *DNFPackageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *DNFPackageResource) refreshState(ctx context.Context, model DNFPackageResourceModel) (DNFPackageResourceModel, bool, error) {
	name := model.Name.ValueString()

	status, err := r.manager.PackageStatus(ctx, name)
	if err != nil {
		return model, false, err
	}

	model.ID = types.StringValue(name)
	if model.Version.IsNull() || model.Version.IsUnknown() {
		model.Version = types.StringValue(versionLatest)
	}
	if model.Autoremove.IsNull() || model.Autoremove.IsUnknown() {
		model.Autoremove = types.BoolValue(true)
	}
	hydrateVersionState(&model, status)

	return model, status.Installed, nil
}

func (r *DNFPackageResource) syncPackage(ctx context.Context, model DNFPackageResourceModel) error {
	name := model.Name.ValueString()

	status, err := r.manager.PackageStatus(ctx, name)
	if err != nil {
		return err
	}

	if !status.Installed {
		if err := r.manager.InstallPackages(ctx, []string{name}); err != nil {
			return err
		}
	} else if shouldUpgradeToLatest(model.Version.ValueString(), status) {
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

func hydrateVersionState(model *DNFPackageResourceModel, status PackageStatus) {
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

func shouldUpgradeToLatest(version string, status PackageStatus) bool {
	return version == versionLatest &&
		status.Installed &&
		status.InstalledVersion != "" &&
		status.UpgradeVersion != "" &&
		status.InstalledVersion != status.UpgradeVersion
}

func validatePackageName(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("package name must be non-empty and must not contain leading or trailing whitespace")
	}

	return nil
}

func validateVersionPolicy(version string) error {
	if version != versionLatest {
		return fmt.Errorf("only %q is currently supported", versionLatest)
	}

	return nil
}
