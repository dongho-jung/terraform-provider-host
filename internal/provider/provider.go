package provider

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &HostProvider{}

type HostProvider struct {
	version string
}

type HostProviderModel struct {
	RuntimeDir    types.String `tfsdk:"runtime_dir"`
	SudoPath      types.String `tfsdk:"sudo_path"`
	DNFPath       types.String `tfsdk:"dnf_path"`
	BrewPath      types.String `tfsdk:"brew_path"`
	GitPath       types.String `tfsdk:"git_path"`
	SSHKeygenPath types.String `tfsdk:"ssh_keygen_path"`
	CrontabPath   types.String `tfsdk:"crontab_path"`
	DefaultsPath  types.String `tfsdk:"defaults_path"`
	KillallPath   types.String `tfsdk:"killall_path"`
	SwiftPath     types.String `tfsdk:"swift_path"`
	OSAScriptPath types.String `tfsdk:"osascript_path"`
}

func (p *HostProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "host"
	resp.Version = p.version
}

func (p *HostProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The Host provider manages host-related infrastructure.",
		Attributes: map[string]schema.Attribute{
			"runtime_dir": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Directory where provider runtime metadata is stored. Defaults to `./.terraform-provider-host` relative to the Terraform working directory.",
			},
			"sudo_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to the `sudo` executable. Defaults to resolving `sudo` from PATH.",
			},
			"dnf_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to the `dnf` executable. Defaults to resolving `dnf` from PATH.",
			},
			"brew_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to the `brew` executable. Defaults to resolving `brew` from PATH.",
			},
			"git_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to the `git` executable. Defaults to resolving `git` from PATH.",
			},
			"ssh_keygen_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to the `ssh-keygen` executable. Defaults to resolving `ssh-keygen` from PATH.",
			},
			"crontab_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to the `crontab` executable. Defaults to resolving `crontab` from PATH.",
			},
			"defaults_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to the macOS `defaults` executable. Defaults to resolving `defaults` from PATH.",
			},
			"killall_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to the macOS `killall` executable. Defaults to resolving `killall` from PATH.",
			},
			"swift_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to the `swift` executable used by CoreAudio helpers. Defaults to resolving `swift` from PATH.",
			},
			"osascript_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to the `osascript` executable used by macOS automation helpers. Defaults to resolving `osascript` from PATH.",
			},
		},
	}
}

func (p *HostProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config HostProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var data HostProviderData

	if !config.RuntimeDir.IsNull() && !config.RuntimeDir.IsUnknown() {
		runtimeDir, err := expandHostPath(config.RuntimeDir.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Invalid runtime_dir", err.Error())
			return
		}
		setProviderRuntimeDir(runtimeDir)
	} else {
		setProviderRuntimeDir("")
	}

	sudoPath, err := configuredExecutablePath("sudo", config.SudoPath)
	if err != nil {
		resp.Diagnostics.AddError("Invalid sudo_path", err.Error())
		return
	}

	dnfPath, err := configuredExecutablePath("dnf", config.DNFPath)
	if err != nil {
		resp.Diagnostics.AddError("Invalid dnf_path", err.Error())
		return
	}
	if dnfPath != "" {
		data.PackageManager = NewCLIPackageManager(dnfPath, sudoPath)
	}

	brewPath, err := configuredExecutablePath("brew", config.BrewPath)
	if err != nil {
		resp.Diagnostics.AddError("Invalid brew_path", err.Error())
		return
	}
	if brewPath != "" {
		data.BrewManager = NewCLIBrewPackageManager(brewPath, sudoPath)
	}

	gitPath, err := configuredExecutablePath("git", config.GitPath)
	if err != nil {
		resp.Diagnostics.AddError("Invalid git_path", err.Error())
		return
	}
	if gitPath != "" {
		data.GitPath = gitPath
	}

	sshKeygenPath, err := configuredExecutablePath("ssh-keygen", config.SSHKeygenPath)
	if err != nil {
		resp.Diagnostics.AddError("Invalid ssh_keygen_path", err.Error())
		return
	}
	if sshKeygenPath != "" {
		data.SSHKeyManager = NewCLISSHKeyManager(sshKeygenPath)
	}

	crontabPath, err := configuredExecutablePath("crontab", config.CrontabPath)
	if err != nil {
		resp.Diagnostics.AddError("Invalid crontab_path", err.Error())
		return
	}
	data.ScheduleManager = NewCLICronScheduleManager(crontabPath, data.PackageManager, sudoPath)
	data.IdentityManager = NewCLIIdentityManager(sudoPath)
	defaultsPath, err := configuredExecutablePath("defaults", config.DefaultsPath)
	if err != nil {
		resp.Diagnostics.AddError("Invalid defaults_path", err.Error())
		return
	}
	if defaultsPath != "" {
		killallPath, err := configuredExecutablePath("killall", config.KillallPath)
		if err != nil {
			resp.Diagnostics.AddError("Invalid killall_path", err.Error())
			return
		}
		data.MacOSDefaultsManager = NewCLIMacOSDefaultsManager(defaultsPath, killallPath)
		data.MacOSDockManager = NewCLIMacOSDockManager(defaultsPath, killallPath)
	}
	swiftPath, err := configuredExecutablePath("swift", config.SwiftPath)
	if err != nil {
		resp.Diagnostics.AddError("Invalid swift_path", err.Error())
		return
	}
	if swiftPath != "" {
		data.MacOSAudioManager = NewCLIMacOSAudioManager(swiftPath)
	}
	osascriptPath, err := configuredExecutablePath("osascript", config.OSAScriptPath)
	if err != nil {
		resp.Diagnostics.AddError("Invalid osascript_path", err.Error())
		return
	}
	if osascriptPath != "" {
		data.MacOSLoginItemManager = NewCLIMacOSLoginItemManager(osascriptPath)
	}

	resp.ResourceData = data
	resp.DataSourceData = data
}

func configuredExecutablePath(name string, configured types.String) (string, error) {
	if !configured.IsNull() && !configured.IsUnknown() {
		path := configured.ValueString()
		if path == "" {
			return "", fmt.Errorf("path must not be empty")
		}
		return path, nil
	}

	path, lookupErr := exec.LookPath(name)
	if lookupErr == nil {
		return path, nil
	}

	return "", nil
}

func (p *HostProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewDNFPackageResource,
		NewBrewPackageResource,
		NewHostDirResource,
		NewHostFileResource,
		NewHostFileBlockResource,
		NewHostGitRepositoryResource,
		NewHostSSHKeyResource,
		NewHostSSHConfigHostResource,
		NewHostLinkResource,
		NewMacOSDefaultResource,
		NewMacOSDefaultsResource,
		NewMacOSDockResource,
		NewMacOSLoginItemResource,
		NewMacOSAudioMultiOutputResource,
		NewHostScheduleResource,
		NewHostGroupResource,
		NewHostUserResource,
	}
}

func (p *HostProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewHostGroupDataSource,
		NewMacOSAudioDeviceDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &HostProvider{
			version: version,
		}
	}
}
