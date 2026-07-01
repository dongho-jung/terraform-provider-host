package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &HostGroupDataSource{}
	_ datasource.DataSourceWithConfigure = &HostGroupDataSource{}
)

type HostGroupDataSource struct {
	manager IdentityManager
}

type HostGroupDataSourceModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
	Role types.String `tfsdk:"role"`
	GID  types.String `tfsdk:"gid"`
}

func NewHostGroupDataSource() datasource.DataSource {
	return &HostGroupDataSource{}
}

func (d *HostGroupDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (d *HostGroupDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads an existing local host group by name or by an operating-system role such as `admin`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Group identifier, equal to `name`.",
			},
			"name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Local group name. When `role` is set, this is resolved by the provider.",
			},
			"role": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Portable role to resolve. Currently only `admin` is supported. It resolves to `admin` on macOS and the first existing `wheel` or `sudo` group on Linux.",
			},
			"gid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Numeric group ID as reported by the operating system.",
			},
		},
	}
}

func (d *HostGroupDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.IdentityManager == nil {
			resp.Diagnostics.AddError("Identity backend unavailable", "`host_group` requires local user/group command line tools.")
			return
		}
		d.manager = data.IdentityManager
	case IdentityManager:
		d.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or IdentityManager, got %T.", req.ProviderData),
		)
	}
}

func (d *HostGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config HostGroupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	hasName := !config.Name.IsNull() && !config.Name.IsUnknown()
	hasRole := !config.Role.IsNull() && !config.Role.IsUnknown()
	if hasName == hasRole {
		resp.Diagnostics.AddError("Invalid host group data source", "Set exactly one of `name` or `role`.")
		return
	}

	var status HostGroupStatus
	if hasRole {
		role := config.Role.ValueString()
		resolved, err := d.manager.ResolveGroupRole(ctx, role)
		if err != nil {
			resp.Diagnostics.AddError("Failed to resolve host group role", err.Error())
			return
		}
		status = resolved
	} else {
		name := config.Name.ValueString()
		resolved, exists, err := d.manager.GroupStatus(ctx, name)
		if err != nil {
			resp.Diagnostics.AddError("Failed to read host group", err.Error())
			return
		}
		if !exists {
			resp.Diagnostics.AddError("Host group not found", fmt.Sprintf("Group %q does not exist.", name))
			return
		}
		status = resolved
	}

	config.ID = types.StringValue(status.Name)
	config.Name = types.StringValue(status.Name)
	config.GID = types.StringValue(status.GID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
