package provider

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

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
		MarkdownDescription: "The Host provider manages system-scoped host infrastructure and optional user-scoped local state.",
		Attributes: map[string]schema.Attribute{
			"runtime_dir": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Directory where user-scoped provider runtime metadata is stored. Requires `target_user` and defaults to `~/.local/state/terraform-provider-host` under that user's home directory.",
			},
			"home_dir": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Home directory used to expand leading `~` in user-scoped host paths. Requires `target_user` and defaults to that user's discovered home directory. Set this when bootstrapping a target user that does not exist yet.",
			},
			"target_user": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Local user context for user-scoped resources. User-scoped resources require this argument and use the user's home directory and crontab. System-scoped resources do not require it.",
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

	if config.TargetUser.IsUnknown() {
		resp.Diagnostics.AddError("Unknown target_user", "`target_user` must be known while configuring the Host provider.")
		return
	}

	if config.TargetUser.IsNull() {
		if !config.HomeDir.IsNull() {
			resp.Diagnostics.AddError("home_dir requires target_user", "`home_dir` belongs to a user context. Configure `target_user`, or omit both arguments for a system-only provider configuration.")
			return
		}
		if !config.RuntimeDir.IsNull() {
			resp.Diagnostics.AddError("runtime_dir requires target_user", "`runtime_dir` stores user-scoped provider metadata. Configure `target_user`, or omit both arguments for a system-only provider configuration.")
			return
		}
	} else {
		targetUser := config.TargetUser.ValueString()
		if err := validateHostUserName(targetUser); err != nil {
			resp.Diagnostics.AddError("Invalid target_user", err.Error())
			return
		}
		data.TargetUser = targetUser

		targetHomeDir, err := resolveTargetUserHomeDir(ctx, data.TargetUser)
		if !config.HomeDir.IsNull() && !config.HomeDir.IsUnknown() {
			homeBase := targetHomeDir
			if err != nil {
				homeBase = ""
			}
			homeDir, err := expandHostPathWithHome(config.HomeDir.ValueString(), homeBase)
			if err != nil {
				resp.Diagnostics.AddError("Invalid home_dir", err.Error())
				return
			}
			data.HomeDir = homeDir
		} else {
			if err != nil {
				resp.Diagnostics.AddError("Invalid target_user", fmt.Sprintf("%s. Set home_dir when bootstrapping a target user that does not exist yet.", err.Error()))
				return
			}
			data.HomeDir = targetHomeDir
		}

		if !config.RuntimeDir.IsNull() && !config.RuntimeDir.IsUnknown() {
			runtimeDir, err := expandHostPathWithHome(config.RuntimeDir.ValueString(), data.HomeDir)
			if err != nil {
				resp.Diagnostics.AddError("Invalid runtime_dir", err.Error())
				return
			}
			data.RuntimeDir = runtimeDir
		} else {
			runtimeDir, err := providerRuntimeDirForHome(data.HomeDir)
			if err != nil {
				resp.Diagnostics.AddError("Invalid runtime_dir", err.Error())
				return
			}
			data.RuntimeDir = runtimeDir
		}
	}

	sudoPath := executablePath("sudo")
	data.IdentityManager = NewCLIIdentityManager(sudoPath)

	dnfPath := executablePath("dnf")
	if dnfPath != "" {
		data.PackageManager = NewCLIPackageManager(dnfPath, sudoPath)
	}

	pacmanPath := executablePath("pacman")
	if pacmanPath != "" {
		pacmanManager := NewCLIPacmanPackageManager(pacmanPath, sudoPath)
		data.PacmanManager = pacmanManager
		if data.TargetUser != "" {
			data.AURManager = NewResolvingAURPackageManager(pacmanManager)
			data.AURHelperManager = NewCLIAURHelperManager(pacmanManager)
		}
	}

	hostnamectlPath := executablePath("hostnamectl")
	scutilPath := executablePath("scutil")
	if hostnamectlPath != "" || scutilPath != "" {
		data.HostnameManager = NewCLIHostnameManager(runtime.GOOS, hostnamectlPath, scutilPath, sudoPath)
	}

	timedatectlPath := executablePath("timedatectl")
	systemsetupPath := executablePath("systemsetup")
	if timedatectlPath != "" || systemsetupPath != "" {
		data.TimezoneManager = NewCLITimezoneManager(runtime.GOOS, timedatectlPath, systemsetupPath, sudoPath)
	}

	localectlPath := executablePath("localectl")
	if localectlPath != "" {
		localectlManager := NewCLILocalectlManager(localectlPath, sudoPath)
		data.LocaleManager = localectlManager
		data.KeymapManager = localectlManager
	}

	systemctlPath := executablePath("systemctl")
	if systemctlPath != "" {
		data.SystemdManager = NewCLISystemdServiceManager(systemctlPath, sudoPath)
		data.SystemdUnitManager = NewCLISystemdUnitManager(systemctlPath, sudoPath)
	}

	sysctlPath := executablePath("sysctl")
	if runtime.GOOS == "linux" && sysctlPath != "" {
		data.SysctlManager = NewCLISysctlManager(sysctlPath, sudoPath)
	}

	if runtime.GOOS == "linux" {
		data.FstabManager = NewHostFstabManager(sudoPath)
	}

	if data.TargetUser != "" {
		data.DesktopSessionValidator = NewHostDesktopSessionValidator()

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

		defaultsPath := executablePath("defaults")
		if defaultsPath != "" {
			killallPath := executablePath("killall")
			data.MacOSDefaultsManager = NewCLIMacOSDefaultsManager(defaultsPath, killallPath)
			data.MacOSDockManager = NewCLIMacOSDockManager(defaultsPath, killallPath)
		}
		swiftCompilerPath := executablePath("swiftc")
		if swiftCompilerPath != "" {
			data.MacOSAudioManager = NewCLIMacOSAudioManager(swiftCompilerPath, data.RuntimeDir)
		}
		osascriptPath := executablePath("osascript")
		if osascriptPath != "" {
			data.MacOSLoginItemManager = NewCLIMacOSLoginItemManager(osascriptPath, data.HomeDir)
		}
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
		NewPacmanPackageResource,
		NewAURHelperResource,
		NewAURPackageResource,
		NewBrewPackageResource,
		NewHostDirResource,
		NewHostFileResource,
		NewHostFileBlockResource,
		NewHostSystemFileResource,
		NewHostSudoersRuleResource,
		NewHostGitRepositoryResource,
		NewHostHostnameResource,
		NewHostTimezoneResource,
		NewHostLocaleResource,
		NewHostKeymapResource,
		NewHostSysctlResource,
		NewHostSystemdUnitResource,
		NewHostSystemdServiceResource,
		NewHostFstabEntryResource,
		NewHostGroupResource,
		NewHostUserResource,
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
	}
}

func (p *HostProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewDNFPackageDataSource,
		NewPacmanPackageDataSource,
		NewAURPackageDataSource,
		NewBrewPackageDataSource,
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
