package provider

import (
	"context"
	"os/exec"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

var _ provider.Provider = &HostProvider{}

type HostProvider struct {
	version string
}

type HostProviderModel struct {
}

func (p *HostProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "host"
	resp.Version = p.version
}

func (p *HostProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The Host provider manages host-related infrastructure.",
	}
}

func (p *HostProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config HostProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var data HostProviderData

	sudoPath, err := exec.LookPath("sudo")
	if err != nil {
		sudoPath = ""
	}

	dnfPath, err := exec.LookPath("dnf")
	if err == nil {
		data.PackageManager = NewCLIPackageManager(dnfPath, sudoPath)
	}

	brewPath, err := exec.LookPath("brew")
	if err == nil {
		data.BrewManager = NewCLIBrewPackageManager(brewPath)
	}

	gitPath, err := exec.LookPath("git")
	if err == nil {
		data.GitPath = gitPath
	}

	crontabPath, err := exec.LookPath("crontab")
	if err != nil {
		crontabPath = ""
	}
	launchctlPath, err := exec.LookPath("launchctl")
	if err != nil {
		launchctlPath = ""
	}
	data.ScheduleManager = NewCLICronScheduleManager(crontabPath, launchctlPath, data.PackageManager, sudoPath)
	data.IdentityManager = NewCLIIdentityManager(sudoPath)
	defaultsPath, err := exec.LookPath("defaults")
	if err == nil {
		killallPath, _ := exec.LookPath("killall")
		data.MacOSDefaultsManager = NewCLIMacOSDefaultsManager(defaultsPath, killallPath)
		data.MacOSDockManager = NewCLIMacOSDockManager(defaultsPath, killallPath)
	}

	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *HostProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewDNFPackageResource,
		NewBrewPackageResource,
		NewHostDirResource,
		NewHostFileResource,
		NewHostFileBlockResource,
		NewHostGitRepositoryResource,
		NewHostLinkResource,
		NewMacOSDefaultResource,
		NewMacOSDefaultsResource,
		NewMacOSDockResource,
		NewHostScheduleResource,
		NewHostGroupResource,
		NewHostUserResource,
	}
}

func (p *HostProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewHostGroupDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &HostProvider{
			version: version,
		}
	}
}
