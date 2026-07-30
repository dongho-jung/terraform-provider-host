package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &AURPackageDataSource{}
	_ datasource.DataSourceWithConfigure = &AURPackageDataSource{}
)

type AURPackageDataSource struct {
	manager AURPackageManager
}

type AURPackageDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	IncludeRemote    types.Bool   `tfsdk:"include_remote"`
	Installed        types.Bool   `tfsdk:"installed"`
	InstallReason    types.String `tfsdk:"install_reason"`
	InstalledVersion types.String `tfsdk:"installed_version"`
	CandidateVersion types.String `tfsdk:"candidate_version"`
}

func NewAURPackageDataSource() datasource.DataSource {
	return &AURPackageDataSource{}
}

func (d *AURPackageDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_package_aur"
}

func (d *AURPackageDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads AUR package installation metadata from Pacman and optionally queries a verified AUR helper for the candidate version.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Package identifier, equal to `name`.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "AUR package name.",
			},
			"include_remote": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Query a verified `yay` or `paru` helper for the candidate AUR version. Defaults to false so local installation metadata can be read without a helper or network access.",
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
				MarkdownDescription: "Installed package version from the local Pacman database. Null when the package is not installed.",
			},
			"candidate_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Latest package version reported by the AUR helper. Null when `include_remote` is false or no candidate is available.",
			},
		},
	}
}

func (d *AURPackageDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if !requireHostUserScope(data, "data.host_package_aur", &resp.Diagnostics) {
			return
		}
		if data.AURManager == nil {
			resp.Diagnostics.AddError(
				"AUR package lookup unavailable",
				"`host_package_aur` requires `pacman` to be available. Remote candidate lookup additionally requires a verified `yay` or `paru` executable.",
			)
			return
		}
		d.manager = data.AURManager
	case AURPackageManager:
		d.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or AURPackageManager, got %T.", req.ProviderData),
		)
	}
}

func (d *AURPackageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config AURPackageDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if d.manager == nil {
		resp.Diagnostics.AddError(
			"AUR package lookup unavailable",
			"`host_package_aur` requires `pacman` to be available. Remote candidate lookup additionally requires a verified `yay` or `paru` executable.",
		)
		return
	}

	name, err := packageDataSourceName(config.Name)
	if err != nil {
		resp.Diagnostics.AddError("Invalid AUR package data source", err.Error())
		return
	}
	includeRemote := !config.IncludeRemote.IsNull() &&
		!config.IncludeRemote.IsUnknown() &&
		config.IncludeRemote.ValueBool()
	status, err := d.manager.PackageStatus(ctx, name, includeRemote)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read AUR package", err.Error())
		return
	}

	config.ID = types.StringValue(name)
	config.IncludeRemote = types.BoolValue(includeRemote)
	config.Installed = types.BoolValue(status.Installed)
	hydratePackageInstallReason(&config.InstallReason, status)
	config.InstalledVersion = packageVersionValue(status.InstalledVersion)
	if includeRemote {
		config.CandidateVersion = packageVersionValue(status.CandidateVersion)
	} else {
		config.CandidateVersion = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
