package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &PacmanPackageDataSource{}
	_ datasource.DataSourceWithConfigure = &PacmanPackageDataSource{}
)

type PacmanPackageDataSource struct {
	manager PackageManager
}

type PacmanPackageDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Installed        types.Bool   `tfsdk:"installed"`
	InstallReason    types.String `tfsdk:"install_reason"`
	InstalledVersion types.String `tfsdk:"installed_version"`
	CandidateVersion types.String `tfsdk:"candidate_version"`
}

func NewPacmanPackageDataSource() datasource.DataSource {
	return &PacmanPackageDataSource{}
}

func (d *PacmanPackageDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_package_pacman"
}

func (d *PacmanPackageDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads Pacman package installation and sync-database metadata without managing the package.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Package identifier, equal to `name`.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Pacman package name.",
			},
			"installed": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether Pacman reports the package as installed.",
			},
			"install_reason": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Observed Pacman install reason: `explicit` or `dependency`. Null when the package is not installed.",
			},
			"installed_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Installed Pacman package version. Null when the package is not installed.",
			},
			"candidate_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Latest package version from Pacman's sync databases. Null when no candidate is available.",
			},
		},
	}
}

func (d *PacmanPackageDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
		d.manager = data.PacmanManager
	case PackageManager:
		d.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or PackageManager, got %T.", req.ProviderData),
		)
	}
}

func (d *PacmanPackageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config PacmanPackageDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if d.manager == nil {
		resp.Diagnostics.AddError("Pacman executable not found", "`host_package_pacman` requires `pacman` to be available in PATH.")
		return
	}

	name, err := packageDataSourceName(config.Name)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Pacman package data source", err.Error())
		return
	}
	status, err := d.manager.PackageStatus(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read Pacman package", err.Error())
		return
	}

	config.ID = types.StringValue(name)
	config.Installed = types.BoolValue(status.Installed)
	hydratePackageInstallReason(&config.InstallReason, status)
	config.InstalledVersion = packageVersionValue(status.InstalledVersion)
	config.CandidateVersion = packageVersionValue(status.CandidateVersion)

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
