package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &MacOSAudioDeviceDataSource{}
	_ datasource.DataSourceWithConfigure = &MacOSAudioDeviceDataSource{}
)

type MacOSAudioDeviceDataSource struct {
	manager MacOSAudioManager
}

type MacOSAudioDeviceDataSourceModel struct {
	ID             types.String `tfsdk:"id"`
	UID            types.String `tfsdk:"uid"`
	Name           types.String `tfsdk:"name"`
	BuiltinOutput  types.String `tfsdk:"builtin_output"`
	Manufacturer   types.String `tfsdk:"manufacturer"`
	InputChannels  types.Int64  `tfsdk:"input_channels"`
	OutputChannels types.Int64  `tfsdk:"output_channels"`
}

func NewMacOSAudioDeviceDataSource() datasource.DataSource {
	return &MacOSAudioDeviceDataSource{}
}

func (d *MacOSAudioDeviceDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mac_audio_device"
}

func (d *MacOSAudioDeviceDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads an existing macOS CoreAudio device by UID, display name, or built-in output selector.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "CoreAudio device UID.",
			},
			"uid": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "CoreAudio device UID. Set exactly one of `uid`, `name`, or `builtin_output`.",
			},
			"name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "CoreAudio device display name. Set exactly one of `uid`, `name`, or `builtin_output`. The provider requires exactly one matching device.",
			},
			"builtin_output": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Built-in output selector. Supported values are `headphones` and `speakers`. Set exactly one of `uid`, `name`, or `builtin_output`.",
			},
			"manufacturer": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "CoreAudio device manufacturer.",
			},
			"input_channels": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Number of input channels reported by CoreAudio.",
			},
			"output_channels": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Number of output channels reported by CoreAudio.",
			},
		},
	}
}

func (d *MacOSAudioDeviceDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.MacOSAudioManager == nil {
			resp.Diagnostics.AddError("macOS audio unavailable", "`host_mac_audio_device` requires the macOS `swift` command to access CoreAudio.")
			return
		}
		d.manager = data.MacOSAudioManager
	case MacOSAudioManager:
		d.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or MacOSAudioManager, got %T.", req.ProviderData),
		)
	}
}

func (d *MacOSAudioDeviceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config MacOSAudioDeviceDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if d.manager == nil {
		resp.Diagnostics.AddError("macOS audio unavailable", "`host_mac_audio_device` requires the macOS `swift` command to access CoreAudio.")
		return
	}

	devices, err := d.manager.ListDevices(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list macOS audio devices", err.Error())
		return
	}

	device, err := resolveMacOSAudioDeviceInfo(devices, MacOSAudioDeviceSelectorModel{
		UID:           config.UID,
		Name:          config.Name,
		BuiltinOutput: config.BuiltinOutput,
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to resolve macOS audio device", err.Error())
		return
	}

	config.ID = types.StringValue(device.UID)
	config.UID = types.StringValue(device.UID)
	config.Name = types.StringValue(device.Name)
	config.Manufacturer = types.StringValue(device.Manufacturer)
	config.InputChannels = types.Int64Value(device.InputChannels)
	config.OutputChannels = types.Int64Value(device.OutputChannels)

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
