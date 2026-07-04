package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &BrewPackageResource{}
	_ resource.ResourceWithConfigure   = &BrewPackageResource{}
	_ resource.ResourceWithImportState = &BrewPackageResource{}
	_ resource.ResourceWithModifyPlan  = &BrewPackageResource{}
)

type BrewPackageResource struct {
	manager BrewPackageManager
}

type BrewPackageResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Tap              types.String `tfsdk:"tap"`
	PackageType      types.String `tfsdk:"package_type"`
	Version          types.String `tfsdk:"version"`
	IgnoreVersion    types.Bool   `tfsdk:"ignore_version"`
	Autoremove       types.Bool   `tfsdk:"autoremove"`
	Zap              types.Bool   `tfsdk:"zap"`
	InstalledVersion types.String `tfsdk:"installed_version"`
	CandidateVersion types.String `tfsdk:"candidate_version"`
	Pinned           types.Bool   `tfsdk:"pinned"`
	AppPath          types.String `tfsdk:"app_path"`
	AppPaths         types.List   `tfsdk:"app_paths"`
}

func NewBrewPackageResource() resource.Resource {
	return &BrewPackageResource{}
}

func (r *BrewPackageResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_package_brew"
}

func (r *BrewPackageResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a single Homebrew formula or cask.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier in `<package_type>:<name>` form.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Homebrew formula name or cask token.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"tap": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Homebrew tap in `owner/repository` form. When set, the provider ensures the tap exists before reading, installing, upgrading, or removing the package.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"package_type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(brewPackageTypeFormula),
				MarkdownDescription: "Homebrew package type. Supported values are `formula` and `cask`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"version": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(versionLatest),
				MarkdownDescription: "Package version policy. Use `latest` to track the latest Homebrew candidate when `ignore_version` is false, or an exact installed version string to reject drift when Homebrew cannot provide that version.",
			},
			"ignore_version": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Ignore available version updates for `version = \"latest\"`. When true, the resource manages package presence without planning upgrades for new candidate versions. Exact `version` values are still enforced.",
			},
			"autoremove": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Run `brew autoremove` after removing a formula. Ignored for casks.",
			},
			"zap": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Use `brew uninstall --zap` when removing a cask. Ignored for formulae.",
			},
			"installed_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Installed Homebrew package version.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"candidate_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Latest Homebrew package version known to Homebrew. Null when `ignore_version` is true and `version` is `latest`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"pinned": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether Homebrew reports the package as pinned.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"app_path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Application bundle path for casks with exactly one `.app` artifact. Null for formulae, casks without app artifacts, or casks with multiple app artifacts.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"app_paths": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Application bundle paths reported by Homebrew cask app artifacts. Empty for formulae and casks without app artifacts.",
			},
		},
	}
}

func (r *BrewPackageResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.BrewManager == nil {
			resp.Diagnostics.AddError(
				"Homebrew executable not found",
				"`host_package_brew` requires `brew` to be available in PATH.",
			)
			return
		}
		r.manager = data.BrewManager
	case BrewPackageManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or BrewPackageManager, got %T.", req.ProviderData),
		)
	}
}

func (r *BrewPackageResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if r.manager == nil {
		return
	}

	if req.Plan.Raw.IsNull() {
		var state BrewPackageResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		r.addCaskPrivilegeWarning(&resp.Diagnostics, state)
		return
	}

	var plan BrewPackageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.Name.IsNull() || plan.Name.IsUnknown() ||
		plan.PackageType.IsNull() || plan.PackageType.IsUnknown() ||
		plan.Version.IsNull() || plan.Version.IsUnknown() {
		return
	}

	if err := validateBrewResourcePlan(plan); err != nil {
		resp.Diagnostics.AddError("Invalid Homebrew package", err.Error())
		return
	}

	tapName := brewPackageTap(plan)
	packageName := brewPackageCommandName(plan)
	if tapName != "" {
		tapInstalled, err := r.manager.TapInstalled(ctx, tapName)
		if err != nil {
			resp.Diagnostics.AddError("Failed to read Homebrew tap", err.Error())
			return
		}
		if !tapInstalled {
			plan.ID = types.StringValue(brewPackageID(plan.PackageType.ValueString(), packageName))
			plan.InstalledVersion = types.StringUnknown()
			plan.AppPath = types.StringUnknown()
			plan.AppPaths = types.ListUnknown(types.StringType)
			if brewPackageIgnoresVersion(plan) {
				plan.CandidateVersion = types.StringNull()
			} else {
				plan.CandidateVersion = types.StringUnknown()
			}
			plan.Pinned = types.BoolUnknown()
			r.addCaskPrivilegeWarning(&resp.Diagnostics, plan)
			resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
			return
		}
	}

	status, err := r.manager.PackageStatus(ctx, packageName, plan.PackageType.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read Homebrew package", err.Error())
		return
	}

	hydrateBrewVersionState(&plan, status)
	hydrateBrewAppPathState(&plan, status)
	plan.ID = types.StringValue(brewPackageID(plan.PackageType.ValueString(), packageName))
	plan.Pinned = types.BoolValue(status.Pinned)
	if brewPackageIgnoresVersion(plan) {
		plan.CandidateVersion = types.StringNull()
	}

	if !brewPackageIgnoresVersion(plan) {
		if err := validateBrewVersionAvailability(plan.Version.ValueString(), status); err != nil {
			resp.Diagnostics.AddError("Unsupported Homebrew package version", err.Error())
			return
		}
	}

	if !brewPackageIgnoresVersion(plan) && status.Pinned && shouldUpgradeBrewPackage(plan.Version.ValueString(), status) {
		resp.Diagnostics.AddError(
			"Homebrew package is pinned",
			fmt.Sprintf("Homebrew package %q is pinned and cannot be upgraded by `brew upgrade`. Run `brew unpin %s` before applying.", plan.Name.ValueString(), plan.Name.ValueString()),
		)
		return
	}

	if !status.Installed {
		r.addCaskPrivilegeWarning(&resp.Diagnostics, plan)
		markBrewVersionStateUnknown(&plan)
		if brewPackageIgnoresVersion(plan) {
			plan.CandidateVersion = types.StringNull()
		}
	} else if !brewPackageIgnoresVersion(plan) && shouldUpgradeBrewPackage(plan.Version.ValueString(), status) {
		r.addCaskPrivilegeWarning(&resp.Diagnostics, plan)
		markBrewVersionStateUnknown(&plan)
	}

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *BrewPackageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan BrewPackageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validateBrewResourcePlan(plan); err != nil {
		resp.Diagnostics.AddError("Invalid Homebrew package", err.Error())
		return
	}

	if err := r.syncPackage(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Failed to sync Homebrew package", err.Error())
		return
	}

	state, _, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read Homebrew package", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BrewPackageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state BrewPackageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	newState, installed, err := r.refreshState(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read Homebrew package", err.Error())
		return
	}

	if !installed {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *BrewPackageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan BrewPackageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validateBrewResourcePlan(plan); err != nil {
		resp.Diagnostics.AddError("Invalid Homebrew package", err.Error())
		return
	}

	if err := r.syncPackage(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Failed to sync Homebrew package", err.Error())
		return
	}

	state, _, err := r.refreshState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read Homebrew package", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BrewPackageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state BrewPackageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validateBrewPackageName(state.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid Homebrew package name", err.Error())
		return
	}
	if !state.Tap.IsNull() && !state.Tap.IsUnknown() {
		if err := validateBrewTapName(state.Tap.ValueString()); err != nil {
			resp.Diagnostics.AddError("Invalid Homebrew tap name", err.Error())
			return
		}
	}
	if err := validateBrewPackageType(state.PackageType.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid Homebrew package type", err.Error())
		return
	}

	if err := r.manager.RemovePackage(ctx, brewPackageCommandName(state), state.PackageType.ValueString(), state.Autoremove.ValueBool(), state.Zap.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Failed to remove Homebrew package", err.Error())
		return
	}
}

func (r *BrewPackageResource) addCaskPrivilegeWarning(diags *diag.Diagnostics, model BrewPackageResourceModel) {
	if model.PackageType.IsNull() || model.PackageType.IsUnknown() || model.PackageType.ValueString() != brewPackageTypeCask {
		return
	}

	reporter, ok := r.manager.(privilegeEscalationReporter)
	if !ok || !reporter.NeedsPrivilegeEscalation() {
		return
	}

	addSudoPrivilegeWarningOnce(diags)
}

func (r *BrewPackageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	packageType, name, err := parseBrewPackageImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Homebrew package import ID", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(brewPackageID(packageType, name)))...)
	if tapName := brewTapFromPackageName(name); tapName != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), types.StringValue(strings.TrimPrefix(name, tapName+"/")))...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("tap"), types.StringValue(tapName))...)
	} else {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), types.StringValue(name))...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("package_type"), types.StringValue(packageType))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("version"), types.StringValue(versionLatest))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("autoremove"), types.BoolValue(true))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("zap"), types.BoolValue(false))...)
}

func (r *BrewPackageResource) refreshState(ctx context.Context, model BrewPackageResourceModel) (BrewPackageResourceModel, bool, error) {
	packageType := model.PackageType.ValueString()
	if packageType == "" {
		packageType = brewPackageTypeFormula
	}

	tapName := brewPackageTap(model)
	if tapName != "" {
		tapInstalled, err := r.manager.TapInstalled(ctx, tapName)
		if err != nil {
			return model, false, err
		}
		if !tapInstalled {
			return model, false, nil
		}
	}

	packageName := brewPackageCommandName(model)
	status, err := r.manager.PackageStatus(ctx, packageName, packageType)
	if err != nil {
		return model, false, err
	}

	model.ID = types.StringValue(brewPackageID(packageType, packageName))
	model.PackageType = types.StringValue(packageType)
	if model.Version.IsNull() || model.Version.IsUnknown() {
		model.Version = types.StringValue(versionLatest)
	}
	if model.IgnoreVersion.IsNull() || model.IgnoreVersion.IsUnknown() {
		model.IgnoreVersion = types.BoolValue(true)
	}
	if model.Autoremove.IsNull() || model.Autoremove.IsUnknown() {
		model.Autoremove = types.BoolValue(true)
	}
	if model.Zap.IsNull() || model.Zap.IsUnknown() {
		model.Zap = types.BoolValue(false)
	}
	model.Pinned = types.BoolValue(status.Pinned)
	hydrateBrewVersionState(&model, status)
	hydrateBrewAppPathState(&model, status)
	if brewPackageIgnoresVersion(model) {
		model.CandidateVersion = types.StringNull()
	}

	return model, status.Installed, nil
}

func (r *BrewPackageResource) syncPackage(ctx context.Context, model BrewPackageResourceModel) error {
	name := brewPackageCommandName(model)
	packageType := model.PackageType.ValueString()

	if tapName := brewPackageTap(model); tapName != "" {
		tapInstalled, err := r.manager.TapInstalled(ctx, tapName)
		if err != nil {
			return err
		}
		if !tapInstalled {
			if err := r.manager.Tap(ctx, tapName); err != nil {
				return err
			}
		}
	}

	status, err := r.manager.PackageStatus(ctx, name, packageType)
	if err != nil {
		return err
	}

	if !brewPackageIgnoresVersion(model) && status.Pinned && shouldUpgradeBrewPackage(model.Version.ValueString(), status) {
		return fmt.Errorf("homebrew package %q is pinned; run `brew unpin %s` before applying", name, name)
	}
	if !brewPackageIgnoresVersion(model) {
		if err := validateBrewVersionAvailability(model.Version.ValueString(), status); err != nil {
			return err
		}
	}

	wasInstalled := status.Installed
	if !status.Installed {
		if err := r.manager.InstallPackage(ctx, name, packageType); err != nil {
			return err
		}
	} else if !brewPackageIgnoresVersion(model) && shouldUpgradeBrewPackage(model.Version.ValueString(), status) {
		if err := r.manager.UpgradePackage(ctx, name, packageType); err != nil {
			return err
		}
	}

	if packageType == brewPackageTypeFormula && wasInstalled && !status.InstalledOnRequest {
		if err := r.manager.MarkPackageOnRequest(ctx, name); err != nil {
			return err
		}
	}

	status, err = r.manager.PackageStatus(ctx, name, packageType)
	if err != nil {
		return err
	}
	if brewPackageIgnoresVersion(model) {
		return nil
	}
	return validateBrewInstalledVersion(model.Version.ValueString(), status)
}

func hydrateBrewVersionState(model *BrewPackageResourceModel, status BrewPackageStatus) {
	model.InstalledVersion = brewVersionValue(status.InstalledVersion)
	model.CandidateVersion = brewVersionValue(status.CandidateVersion)
}

func hydrateBrewAppPathState(model *BrewPackageResourceModel, status BrewPackageStatus) {
	model.AppPath, model.AppPaths = brewAppPathValues(status)
}

func brewAppPathValues(status BrewPackageStatus) (types.String, types.List) {
	if status.PackageType != brewPackageTypeCask {
		return types.StringNull(), types.ListValueMust(types.StringType, nil)
	}

	elements := make([]attr.Value, 0, len(status.AppPaths))
	for _, appPath := range status.AppPaths {
		elements = append(elements, types.StringValue(appPath))
	}
	appPaths := types.ListValueMust(types.StringType, elements)
	if len(status.AppPaths) == 1 {
		return types.StringValue(status.AppPaths[0]), appPaths
	}
	return types.StringNull(), appPaths
}

func markBrewVersionStateUnknown(model *BrewPackageResourceModel) {
	model.InstalledVersion = types.StringUnknown()
	model.CandidateVersion = types.StringUnknown()
}

func brewPackageIgnoresVersion(model BrewPackageResourceModel) bool {
	if !model.Version.IsNull() && !model.Version.IsUnknown() {
		version := model.Version.ValueString()
		if version != "" && version != versionLatest {
			return false
		}
	}

	return model.IgnoreVersion.IsNull() || model.IgnoreVersion.IsUnknown() || model.IgnoreVersion.ValueBool()
}

func shouldUpgradeBrewPackage(version string, status BrewPackageStatus) bool {
	if !status.Installed || status.InstalledVersion == "" || status.UpgradeVersion == "" || status.InstalledVersion == status.UpgradeVersion {
		return false
	}
	if version == versionLatest {
		return true
	}

	return version != "" && version == status.UpgradeVersion
}

func validateBrewResourcePlan(model BrewPackageResourceModel) error {
	if err := validateBrewPackageName(model.Name.ValueString()); err != nil {
		return err
	}
	if !model.Tap.IsNull() && !model.Tap.IsUnknown() {
		if err := validateBrewTapName(model.Tap.ValueString()); err != nil {
			return err
		}
		if inferredTap := brewTapFromPackageName(model.Name.ValueString()); inferredTap != "" && inferredTap != model.Tap.ValueString() {
			return fmt.Errorf("tap %q does not match package name %q", model.Tap.ValueString(), model.Name.ValueString())
		}
	}
	if err := validateBrewPackageType(model.PackageType.ValueString()); err != nil {
		return err
	}
	if err := validateBrewVersionPolicy(model.Version.ValueString()); err != nil {
		return err
	}

	return nil
}

func validateBrewVersionPolicy(version string) error {
	if strings.TrimSpace(version) != version || version == "" {
		return fmt.Errorf("version must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(version, "\r\n") {
		return fmt.Errorf("version must not contain newlines")
	}

	return nil
}

func validateBrewVersionAvailability(version string, status BrewPackageStatus) error {
	if version == versionLatest {
		return nil
	}
	if status.Installed && status.InstalledVersion == version {
		return nil
	}
	if status.UpgradeVersion == version {
		return nil
	}
	if !status.Installed && status.CandidateVersion == version {
		return nil
	}
	if status.CandidateVersion == "" {
		return fmt.Errorf("homebrew did not report a candidate version for %q", status.Name)
	}

	return fmt.Errorf("requested version %q for %s %q, but Homebrew reports installed=%q candidate=%q upgrade=%q; this provider can install or upgrade only to versions Homebrew currently provides", version, status.PackageType, status.Name, status.InstalledVersion, status.CandidateVersion, status.UpgradeVersion)
}

func validateBrewInstalledVersion(version string, status BrewPackageStatus) error {
	if version == versionLatest {
		return nil
	}
	if !status.Installed {
		return fmt.Errorf("homebrew package %q is not installed after sync", status.Name)
	}
	if status.InstalledVersion != version {
		return fmt.Errorf("homebrew package %q installed version is %q, want %q", status.Name, status.InstalledVersion, version)
	}

	return nil
}

func validateBrewPackageName(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("package name must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(name, "\r\n") {
		return fmt.Errorf("package name must not contain newlines")
	}

	return nil
}

func validateBrewTapName(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("tap name must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(name, "\r\n") {
		return fmt.Errorf("tap name must not contain newlines")
	}
	parts := strings.Split(name, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("tap name must be in `owner/repository` form")
	}

	return nil
}

func validateBrewPackageType(packageType string) error {
	switch packageType {
	case brewPackageTypeFormula, brewPackageTypeCask:
		return nil
	default:
		return fmt.Errorf("supported package types are %q and %q", brewPackageTypeFormula, brewPackageTypeCask)
	}
}

func brewPackageID(packageType string, name string) string {
	return packageType + ":" + name
}

func brewPackageCommandName(model BrewPackageResourceModel) string {
	name := model.Name.ValueString()
	if strings.Contains(name, "/") {
		return name
	}
	if tapName := brewPackageTap(model); tapName != "" {
		return tapName + "/" + name
	}

	return name
}

func brewPackageTap(model BrewPackageResourceModel) string {
	if !model.Tap.IsNull() && !model.Tap.IsUnknown() {
		return model.Tap.ValueString()
	}

	return brewTapFromPackageName(model.Name.ValueString())
}

func brewTapFromPackageName(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" {
		return ""
	}

	return parts[0] + "/" + parts[1]
}

func parseBrewPackageImportID(id string) (string, string, error) {
	if strings.TrimSpace(id) != id || id == "" {
		return "", "", fmt.Errorf("import ID must be a package name or `<package_type>:<name>`")
	}

	packageType, name, ok := strings.Cut(id, ":")
	if !ok {
		packageType = brewPackageTypeFormula
		name = id
	}

	if err := validateBrewPackageType(packageType); err != nil {
		return "", "", err
	}
	if err := validateBrewPackageName(name); err != nil {
		return "", "", err
	}

	return packageType, name, nil
}
