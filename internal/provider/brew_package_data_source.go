package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &BrewPackageDataSource{}
	_ datasource.DataSourceWithConfigure = &BrewPackageDataSource{}
)

type BrewPackageDataSource struct {
	manager BrewPackageManager
}

type BrewPackageDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Tap              types.String `tfsdk:"tap"`
	PackageType      types.String `tfsdk:"package_type"`
	Installed        types.Bool   `tfsdk:"installed"`
	InstalledVersion types.String `tfsdk:"installed_version"`
	CandidateVersion types.String `tfsdk:"candidate_version"`
	Pinned           types.Bool   `tfsdk:"pinned"`
	AppPath          types.String `tfsdk:"app_path"`
	AppPaths         types.List   `tfsdk:"app_paths"`
}

func NewBrewPackageDataSource() datasource.DataSource {
	return &BrewPackageDataSource{}
}

func (d *BrewPackageDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_package_brew"
}

func (d *BrewPackageDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads Homebrew formula or cask metadata from the local Homebrew installation.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Package identifier in `<package_type>:<name>` form.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Homebrew formula name or cask token.",
			},
			"tap": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Homebrew tap in `owner/repository` form. When set, the tap must already exist.",
			},
			"package_type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Homebrew package type. Supported values are `formula` and `cask`. Defaults to `formula`.",
			},
			"installed": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether Homebrew reports the package as installed.",
			},
			"installed_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Installed Homebrew package version.",
			},
			"candidate_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Latest Homebrew package version known to Homebrew.",
			},
			"pinned": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether Homebrew reports the package as pinned.",
			},
			"app_path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Application bundle path for casks with exactly one `.app` artifact. Null for formulae, casks without app artifacts, or casks with multiple app artifacts.",
			},
			"app_paths": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Application bundle paths reported by Homebrew cask app artifacts. Empty for formulae and casks without app artifacts.",
			},
		},
	}
}

func (d *BrewPackageDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
		d.manager = data.BrewManager
	case BrewPackageManager:
		d.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or BrewPackageManager, got %T.", req.ProviderData),
		)
	}
}

func (d *BrewPackageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config BrewPackageDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if d.manager == nil {
		resp.Diagnostics.AddError("Homebrew executable not found", "`host_package_brew` requires `brew` to be available in PATH.")
		return
	}

	packageType := brewPackageTypeFormula
	if !config.PackageType.IsNull() && !config.PackageType.IsUnknown() {
		packageType = config.PackageType.ValueString()
	}
	model := BrewPackageResourceModel{
		Name:        config.Name,
		Tap:         config.Tap,
		PackageType: types.StringValue(packageType),
	}
	if err := validateBrewDataSourceConfig(model); err != nil {
		resp.Diagnostics.AddError("Invalid Homebrew package data source", err.Error())
		return
	}

	tapName := brewPackageTap(model)
	if tapName != "" {
		tapInstalled, err := d.manager.TapInstalled(ctx, tapName)
		if err != nil {
			resp.Diagnostics.AddError("Failed to read Homebrew tap", err.Error())
			return
		}
		if !tapInstalled {
			resp.Diagnostics.AddError("Homebrew tap not found", fmt.Sprintf("Tap %q is not installed.", tapName))
			return
		}
	}

	packageName := brewPackageCommandName(model)
	status, err := d.manager.PackageStatus(ctx, packageName, packageType)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read Homebrew package", err.Error())
		return
	}

	config.ID = types.StringValue(brewPackageID(packageType, packageName))
	config.PackageType = types.StringValue(packageType)
	config.Installed = types.BoolValue(status.Installed)
	config.Pinned = types.BoolValue(status.Pinned)
	config.InstalledVersion = brewVersionValue(status.InstalledVersion)
	config.CandidateVersion = brewVersionValue(status.CandidateVersion)
	config.AppPath, config.AppPaths = brewAppPathValues(status)

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func validateBrewDataSourceConfig(model BrewPackageResourceModel) error {
	if model.Name.IsNull() || model.Name.IsUnknown() {
		return fmt.Errorf("name must be known")
	}
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
	return nil
}

func brewVersionValue(version string) types.String {
	if version == "" {
		return types.StringNull()
	}
	return types.StringValue(version)
}
