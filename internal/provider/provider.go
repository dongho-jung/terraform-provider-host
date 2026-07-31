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
	RuntimeDir                types.String `tfsdk:"runtime_dir"`
	HomeDir                   types.String `tfsdk:"home_dir"`
	TargetUser                types.String `tfsdk:"target_user"`
	AURHelper                 types.String `tfsdk:"aur_helper"`
	AURHelperPackage          types.String `tfsdk:"aur_helper_package"`
	AURRemoveMakeDependencies types.Bool   `tfsdk:"aur_remove_make_dependencies"`
	AURCleanAfter             types.Bool   `tfsdk:"aur_clean_after"`
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
			"aur_helper": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "AUR helper to bootstrap lazily when an AUR package mutation needs one. Supported values are `yay` and `paru`; missing `base-devel` and `git` prerequisites are installed first. Requires `target_user`. Planning, refresh, and data-source reads never bootstrap tools. When omitted, AUR package resources only use an already installed, verified helper.",
			},
			"aur_helper_package": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "AUR package that provides the configured `aur_helper`. Defaults to the helper name; set this for variants such as `yay-bin`.",
			},
			"aur_remove_make_dependencies": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Pass `--removemake` to `yay` or `paru` package installs and upgrades so build-only dependencies installed by the helper are removed after a successful build. Requires `target_user` and defaults to false.",
			},
			"aur_clean_after": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Pass `--cleanafter` to `yay` or `paru` package installs and upgrades so package source and build directories are removed after a successful build. Requires `target_user` and defaults to false.",
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
	if config.AURHelper.IsUnknown() {
		resp.Diagnostics.AddError("Unknown aur_helper", "`aur_helper` must be known while configuring the Host provider.")
		return
	}
	if config.AURHelperPackage.IsUnknown() {
		resp.Diagnostics.AddError("Unknown aur_helper_package", "`aur_helper_package` must be known while configuring the Host provider.")
		return
	}
	if config.AURRemoveMakeDependencies.IsUnknown() {
		resp.Diagnostics.AddError("Unknown aur_remove_make_dependencies", "`aur_remove_make_dependencies` must be known while configuring the Host provider.")
		return
	}
	if config.AURCleanAfter.IsUnknown() {
		resp.Diagnostics.AddError("Unknown aur_clean_after", "`aur_clean_after` must be known while configuring the Host provider.")
		return
	}

	aurOptions := AURPackageOptions{
		RemoveMakeDependencies: !config.AURRemoveMakeDependencies.IsNull() && config.AURRemoveMakeDependencies.ValueBool(),
		CleanAfter:             !config.AURCleanAfter.IsNull() && config.AURCleanAfter.ValueBool(),
	}

	var aurHelperSpec *AURHelperSpec
	if config.AURHelper.IsNull() {
		if !config.AURHelperPackage.IsNull() {
			resp.Diagnostics.AddError("aur_helper_package requires aur_helper", "Configure `aur_helper`, or omit `aur_helper_package`.")
			return
		}
	} else {
		if config.TargetUser.IsNull() {
			resp.Diagnostics.AddError("aur_helper requires target_user", "AUR helpers run as an unprivileged user. Configure `target_user` together with `aur_helper`.")
			return
		}
		helperName := config.AURHelper.ValueString()
		helperPackage := helperName
		if !config.AURHelperPackage.IsNull() {
			helperPackage = config.AURHelperPackage.ValueString()
		}
		spec := AURHelperSpec{Name: helperName, Package: helperPackage}
		if err := validateAURHelperSpec(spec); err != nil {
			resp.Diagnostics.AddError("Invalid AUR helper configuration", err.Error())
			return
		}
		aurHelperSpec = &spec
	}

	if config.TargetUser.IsNull() {
		if !config.AURRemoveMakeDependencies.IsNull() || !config.AURCleanAfter.IsNull() {
			resp.Diagnostics.AddError("AUR cleanup requires target_user", "AUR helpers run as an unprivileged user. Configure `target_user` with the AUR cleanup settings, or omit those settings.")
			return
		}
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
			helperManager := NewCLIAURHelperManager(pacmanManager)
			if aurHelperSpec == nil {
				data.AURManager = NewResolvingAURPackageManager(pacmanManager, aurOptions)
			} else {
				data.AURManager = NewBootstrappingAURPackageManager(pacmanManager, helperManager, *aurHelperSpec, aurOptions)
			}
			data.AURHelperManager = helperManager
		}
	} else if aurHelperSpec != nil {
		resp.Diagnostics.AddError("aur_helper requires Pacman", "`aur_helper` can only be configured on a host where the `pacman` executable is available.")
		return
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
