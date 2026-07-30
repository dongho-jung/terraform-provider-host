package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &DNFPackageDataSource{}
	_ datasource.DataSourceWithConfigure = &DNFPackageDataSource{}
)

type DNFPackageDataSource struct {
	manager PackageManager
}

type DNFPackageDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Installed        types.Bool   `tfsdk:"installed"`
	InstallReason    types.String `tfsdk:"install_reason"`
	InstalledVersion types.String `tfsdk:"installed_version"`
	CandidateVersion types.String `tfsdk:"candidate_version"`
}

func NewDNFPackageDataSource() datasource.DataSource {
	return &DNFPackageDataSource{}
}

func (d *DNFPackageDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_package_dnf"
}

func (d *DNFPackageDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads DNF package installation and repository metadata without managing the package.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Package identifier, equal to `name`.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "DNF package name.",
			},
			"installed": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether DNF reports the package as installed.",
			},
			"install_reason": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Normalized DNF install reason, such as `user`, `dependency`, `weak dependency`, or `group`. Null when the package is not installed.",
			},
			"installed_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Installed DNF EVR. Null when the package is not installed.",
			},
			"candidate_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Latest DNF EVR from enabled repositories. Null when no candidate is available.",
			},
		},
	}
}

func (d *DNFPackageDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
		d.manager = data.PackageManager
	case PackageManager:
		d.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or PackageManager, got %T.", req.ProviderData),
		)
	}
}

func (d *DNFPackageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config DNFPackageDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if d.manager == nil {
		resp.Diagnostics.AddError("DNF executable not found", "`host_package_dnf` requires `dnf` to be available in PATH.")
		return
	}

	name, err := packageDataSourceName(config.Name)
	if err != nil {
		resp.Diagnostics.AddError("Invalid DNF package data source", err.Error())
		return
	}
	status, err := d.manager.PackageStatus(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read DNF package", err.Error())
		return
	}

	config.ID = types.StringValue(name)
	config.Installed = types.BoolValue(status.Installed)
	hydrateDNFPackageInstallReason(&config.InstallReason, status)
	config.InstalledVersion = packageVersionValue(status.InstalledVersion)
	config.CandidateVersion = packageVersionValue(status.CandidateVersion)

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
