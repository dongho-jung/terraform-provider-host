package provider

import (
	"context"
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
	RuntimeDir types.String `tfsdk:"runtime_dir"`
	HomeDir    types.String `tfsdk:"home_dir"`
	TargetUser types.String `tfsdk:"target_user"`
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
			"home_dir": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Home directory used to expand leading `~` in host paths. Defaults to the `target_user` home directory when set, otherwise the Terraform process user's home directory.",
			},
			"target_user": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Default target user for user-scoped resources. When `home_dir` is unset, the provider also uses this user's home directory for leading `~` expansion.",
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

	if !config.TargetUser.IsNull() && !config.TargetUser.IsUnknown() {
		targetUser := config.TargetUser.ValueString()
		if err := validateHostUserName(targetUser); err != nil {
			resp.Diagnostics.AddError("Invalid target_user", err.Error())
			return
		}
		data.TargetUser = targetUser
	}

	if !config.HomeDir.IsNull() && !config.HomeDir.IsUnknown() {
		homeDir, err := resolveProviderHomeDir(config.HomeDir.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Invalid home_dir", err.Error())
			return
		}
		data.HomeDir = homeDir
	} else if data.TargetUser != "" {
		homeDir, err := resolveTargetUserHomeDir(ctx, data.TargetUser)
		if err != nil {
			resp.Diagnostics.AddError("Invalid target_user", err.Error())
			return
		}
		data.HomeDir = homeDir
	}

	if !config.RuntimeDir.IsNull() && !config.RuntimeDir.IsUnknown() {
		runtimeDir, err := expandHostPathForHome(config.RuntimeDir.ValueString(), data.HomeDir)
		if err != nil {
			resp.Diagnostics.AddError("Invalid runtime_dir", err.Error())
			return
		}
		data.RuntimeDir = runtimeDir
	} else {
		runtimeDir, err := providerRuntimeDir()
		if err != nil {
			resp.Diagnostics.AddError("Invalid runtime_dir", err.Error())
			return
		}
		data.RuntimeDir = runtimeDir
	}

	sudoPath := executablePath("sudo")
	dnfPath := executablePath("dnf")
	if dnfPath != "" {
		data.PackageManager = NewCLIPackageManager(dnfPath, sudoPath)
	}

	brewPath := executablePath("brew")
	if brewPath != "" {
		data.BrewManager = NewCLIBrewPackageManager(brewPath, sudoPath)
	}

	gitPath := executablePath("git")
	if gitPath != "" {
		data.GitPath = gitPath
	}

	sshKeygenPath := executablePath("ssh-keygen")
	if sshKeygenPath != "" {
		data.SSHKeyManager = NewCLISSHKeyManager(sshKeygenPath, data.HomeDir)
	}

	crontabPath := executablePath("crontab")
	data.ScheduleManager = NewCLICronScheduleManager(crontabPath, data.PackageManager, sudoPath, CLICronScheduleManagerOptions{
		HomeDir:    data.HomeDir,
		RuntimeDir: data.RuntimeDir,
		TargetUser: data.TargetUser,
	})
	data.IdentityManager = NewCLIIdentityManager(sudoPath)
	defaultsPath := executablePath("defaults")
	if defaultsPath != "" {
		killallPath := executablePath("killall")
		data.MacOSDefaultsManager = NewCLIMacOSDefaultsManager(defaultsPath, killallPath)
		data.MacOSDockManager = NewCLIMacOSDockManager(defaultsPath, killallPath)
	}
	swiftPath := executablePath("swift")
	if swiftPath != "" {
		data.MacOSAudioManager = NewCLIMacOSAudioManager(swiftPath)
	}
	osascriptPath := executablePath("osascript")
	if osascriptPath != "" {
		data.MacOSLoginItemManager = NewCLIMacOSLoginItemManager(osascriptPath, data.HomeDir)
	}

	resp.ResourceData = data
	resp.DataSourceData = data
}

func executablePath(name string) string {
	path, lookupErr := exec.LookPath(name)
	if lookupErr == nil {
		return path
	}

	return ""
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
		NewMacOSDockAppResource,
		NewMacOSDockFolderResource,
		NewMacOSLoginItemResource,
		NewMacOSAudioMultiOutputResource,
		NewHostScheduleResource,
		NewHostGroupResource,
		NewHostUserResource,
	}
}

func (p *HostProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewBrewPackageDataSource,
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
