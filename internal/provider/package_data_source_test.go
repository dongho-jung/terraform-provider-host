package provider

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestProviderRegistersCompletePackageDataSourceFamily(t *testing.T) {
	t.Parallel()

	provider := &HostProvider{}
	dataSourceTypes := make(map[string]struct{})
	for _, factory := range provider.DataSources(t.Context()) {
		dataSource := factory()
		var resp datasource.MetadataResponse
		dataSource.Metadata(t.Context(), datasource.MetadataRequest{ProviderTypeName: "host"}, &resp)
		if _, exists := dataSourceTypes[resp.TypeName]; exists {
			t.Fatalf("duplicate data source type %q", resp.TypeName)
		}
		dataSourceTypes[resp.TypeName] = struct{}{}
	}

	packageResourceTypes := make(map[string]struct{})
	for _, factory := range provider.Resources(t.Context()) {
		resource := factory()
		var resp frameworkresource.MetadataResponse
		resource.Metadata(t.Context(), frameworkresource.MetadataRequest{ProviderTypeName: "host"}, &resp)
		if strings.HasPrefix(resp.TypeName, "host_package_") {
			packageResourceTypes[resp.TypeName] = struct{}{}
		}
	}

	packageDataSourceTypes := make(map[string]struct{})
	for typeName := range dataSourceTypes {
		if strings.HasPrefix(typeName, "host_package_") {
			packageDataSourceTypes[typeName] = struct{}{}
		}
	}
	if !reflect.DeepEqual(packageDataSourceTypes, packageResourceTypes) {
		t.Fatalf("package data sources %#v do not match package resources %#v", packageDataSourceTypes, packageResourceTypes)
	}
	if _, exists := dataSourceTypes["host_mac_audio_device"]; !exists {
		t.Fatal("host_mac_audio_device data source is not registered")
	}
}

func TestDNFPackageDataSourceRead(t *testing.T) {
	t.Parallel()

	dataSource := &DNFPackageDataSource{
		manager: fakePackageManager{
			statuses: map[string]PackageStatus{
				"git": {
					Name:             "git",
					Installed:        true,
					ReasonUser:       true,
					InstallReason:    dnfPackageInstallReasonUser,
					InstalledVersion: "2.51.0-1.fc44",
					CandidateVersion: "2.52.0-1.fc44",
				},
			},
		},
	}
	config := DNFPackageDataSourceModel{
		ID:               types.StringUnknown(),
		Name:             types.StringValue("git"),
		Installed:        types.BoolUnknown(),
		InstallReason:    types.StringUnknown(),
		InstalledVersion: types.StringUnknown(),
		CandidateVersion: types.StringUnknown(),
	}
	var state DNFPackageDataSourceModel
	readDataSourceForTest(t, dataSource, &config, &state)

	if state.ID.ValueString() != "git" || !state.Installed.ValueBool() {
		t.Fatalf("unexpected identity/install state: %#v", state)
	}
	if state.InstallReason.ValueString() != dnfPackageInstallReasonUser {
		t.Fatalf("install reason %q, want user", state.InstallReason.ValueString())
	}
	if state.InstalledVersion.ValueString() != "2.51.0-1.fc44" {
		t.Fatalf("installed version %q", state.InstalledVersion.ValueString())
	}
	if state.CandidateVersion.ValueString() != "2.52.0-1.fc44" {
		t.Fatalf("candidate version %q", state.CandidateVersion.ValueString())
	}
}

func TestPacmanPackageDataSourceReadDependency(t *testing.T) {
	t.Parallel()

	dataSource := &PacmanPackageDataSource{
		manager: fakePackageManager{
			statuses: map[string]PackageStatus{
				"glib2": {
					Name:             "glib2",
					Installed:        true,
					InstalledVersion: "2.84.4-1",
					CandidateVersion: "2.86.0-1",
				},
			},
		},
	}
	config := PacmanPackageDataSourceModel{
		ID:               types.StringUnknown(),
		Name:             types.StringValue("glib2"),
		Installed:        types.BoolUnknown(),
		InstallReason:    types.StringUnknown(),
		InstalledVersion: types.StringUnknown(),
		CandidateVersion: types.StringUnknown(),
	}
	var state PacmanPackageDataSourceModel
	readDataSourceForTest(t, dataSource, &config, &state)

	if state.InstallReason.ValueString() != packageInstallReasonDependency {
		t.Fatalf("install reason %q, want dependency", state.InstallReason.ValueString())
	}
	if state.InstalledVersion.ValueString() != "2.84.4-1" {
		t.Fatalf("installed version %q", state.InstalledVersion.ValueString())
	}
	if state.CandidateVersion.ValueString() != "2.86.0-1" {
		t.Fatalf("candidate version %q", state.CandidateVersion.ValueString())
	}
}

func TestPacmanPackageDataSourceReadMissingPackage(t *testing.T) {
	t.Parallel()

	dataSource := &PacmanPackageDataSource{
		manager: fakePackageManager{statuses: map[string]PackageStatus{}},
	}
	config := PacmanPackageDataSourceModel{
		ID:               types.StringUnknown(),
		Name:             types.StringValue("not-installed"),
		Installed:        types.BoolUnknown(),
		InstallReason:    types.StringUnknown(),
		InstalledVersion: types.StringUnknown(),
		CandidateVersion: types.StringUnknown(),
	}
	var state PacmanPackageDataSourceModel
	readDataSourceForTest(t, dataSource, &config, &state)

	if state.Installed.ValueBool() {
		t.Fatal("missing package reported as installed")
	}
	if !state.InstallReason.IsNull() || !state.InstalledVersion.IsNull() || !state.CandidateVersion.IsNull() {
		t.Fatalf("missing package metadata should be null: %#v", state)
	}
}

func TestAURPackageDataSourceSkipsRemoteLookupByDefault(t *testing.T) {
	t.Parallel()

	manager := &fakeAURPackageManager{
		statuses: map[string]PackageStatus{
			"wl-kbptr": {
				Name:             "wl-kbptr",
				Installed:        true,
				ReasonUser:       true,
				InstalledVersion: "0.4.1-2",
				CandidateVersion: "should-not-leak",
			},
		},
	}
	dataSource := &AURPackageDataSource{manager: manager}
	config := AURPackageDataSourceModel{
		ID:               types.StringUnknown(),
		Name:             types.StringValue("wl-kbptr"),
		IncludeRemote:    types.BoolNull(),
		Installed:        types.BoolUnknown(),
		InstallReason:    types.StringUnknown(),
		InstalledVersion: types.StringUnknown(),
		CandidateVersion: types.StringUnknown(),
	}
	var state AURPackageDataSourceModel
	readDataSourceForTest(t, dataSource, &config, &state)

	if state.IncludeRemote.ValueBool() {
		t.Fatal("include_remote should default to false")
	}
	if !state.CandidateVersion.IsNull() {
		t.Fatalf("candidate version should be null without remote lookup: %#v", state.CandidateVersion)
	}
	if want := []bool{false}; !reflect.DeepEqual(manager.remoteLookups, want) {
		t.Fatalf("remote lookup flags %#v, want %#v", manager.remoteLookups, want)
	}
}

func TestAURPackageDataSourceIncludesRemoteCandidateWhenRequested(t *testing.T) {
	t.Parallel()

	manager := &fakeAURPackageManager{
		statuses: map[string]PackageStatus{
			"wl-kbptr": {
				Name:             "wl-kbptr",
				Installed:        true,
				ReasonUser:       true,
				InstalledVersion: "0.4.1-2",
				CandidateVersion: "0.4.2-1",
			},
		},
	}
	dataSource := &AURPackageDataSource{manager: manager}
	config := AURPackageDataSourceModel{
		ID:               types.StringUnknown(),
		Name:             types.StringValue("wl-kbptr"),
		IncludeRemote:    types.BoolValue(true),
		Installed:        types.BoolUnknown(),
		InstallReason:    types.StringUnknown(),
		InstalledVersion: types.StringUnknown(),
		CandidateVersion: types.StringUnknown(),
	}
	var state AURPackageDataSourceModel
	readDataSourceForTest(t, dataSource, &config, &state)

	if state.CandidateVersion.ValueString() != "0.4.2-1" {
		t.Fatalf("candidate version %q", state.CandidateVersion.ValueString())
	}
	if want := []bool{true}; !reflect.DeepEqual(manager.remoteLookups, want) {
		t.Fatalf("remote lookup flags %#v, want %#v", manager.remoteLookups, want)
	}
}

func TestAURPackageDataSourceReportsUnavailableRemoteHelper(t *testing.T) {
	t.Parallel()

	manager := &fakeAURPackageManager{
		statuses: map[string]PackageStatus{
			"wl-kbptr": {Name: "wl-kbptr"},
		},
		statusErr: errors.Join(errAURHelperUnavailable, errors.New("yay not found")),
	}
	dataSource := &AURPackageDataSource{manager: manager}
	config := AURPackageDataSourceModel{
		ID:               types.StringUnknown(),
		Name:             types.StringValue("wl-kbptr"),
		IncludeRemote:    types.BoolValue(true),
		Installed:        types.BoolUnknown(),
		InstallReason:    types.StringUnknown(),
		InstalledVersion: types.StringUnknown(),
		CandidateVersion: types.StringUnknown(),
	}

	resp := readDataSourceResponseForTest(t, dataSource, &config)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected missing AUR helper diagnostic")
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "AUR helper unavailable") {
		t.Fatalf("unexpected diagnostic: %v", resp.Diagnostics)
	}
}

func TestBrewPackageDataSourceReadCaskMetadata(t *testing.T) {
	t.Parallel()

	dataSource := &BrewPackageDataSource{
		manager: &fakeBrewPackageManager{
			statuses: map[string]BrewPackageStatus{
				"cask:hammerspoon": {
					Name:               "hammerspoon",
					PackageType:        brewPackageTypeCask,
					Installed:          true,
					InstalledOnRequest: true,
					InstalledVersion:   "1.1.1",
					CandidateVersion:   "1.2.0",
					AppPaths:           []string{"/Applications/Hammerspoon.app"},
				},
			},
		},
	}
	config := BrewPackageDataSourceModel{
		ID:               types.StringUnknown(),
		Name:             types.StringValue("hammerspoon"),
		Tap:              types.StringNull(),
		PackageType:      types.StringValue(brewPackageTypeCask),
		Installed:        types.BoolUnknown(),
		InstallReason:    types.StringUnknown(),
		InstalledVersion: types.StringUnknown(),
		CandidateVersion: types.StringUnknown(),
		Pinned:           types.BoolUnknown(),
		AppPath:          types.StringUnknown(),
		AppPaths:         types.ListUnknown(types.StringType),
	}
	var state BrewPackageDataSourceModel
	readDataSourceForTest(t, dataSource, &config, &state)

	if state.AppPath.ValueString() != "/Applications/Hammerspoon.app" {
		t.Fatalf("app path %q", state.AppPath.ValueString())
	}
	if state.InstallReason.ValueString() != brewPackageInstallReasonOnRequest {
		t.Fatalf("install reason %q, want on_request", state.InstallReason.ValueString())
	}
	var appPaths []string
	if diags := state.AppPaths.ElementsAs(t.Context(), &appPaths, false); diags.HasError() {
		t.Fatalf("decode app paths: %v", diags)
	}
	if !reflect.DeepEqual(appPaths, []string{"/Applications/Hammerspoon.app"}) {
		t.Fatalf("app paths %#v", appPaths)
	}
}

func TestPackageDataSourcesRejectInvalidNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dataSource datasource.DataSource
		config     any
	}{
		{
			name:       "dnf",
			dataSource: &DNFPackageDataSource{manager: fakePackageManager{}},
			config: &DNFPackageDataSourceModel{
				ID:               types.StringUnknown(),
				Name:             types.StringValue("-bad"),
				Installed:        types.BoolUnknown(),
				InstallReason:    types.StringUnknown(),
				InstalledVersion: types.StringUnknown(),
				CandidateVersion: types.StringUnknown(),
			},
		},
		{
			name:       "pacman",
			dataSource: &PacmanPackageDataSource{manager: fakePackageManager{}},
			config: &PacmanPackageDataSourceModel{
				ID:               types.StringUnknown(),
				Name:             types.StringValue("bad/name"),
				Installed:        types.BoolUnknown(),
				InstallReason:    types.StringUnknown(),
				InstalledVersion: types.StringUnknown(),
				CandidateVersion: types.StringUnknown(),
			},
		},
		{
			name:       "aur",
			dataSource: &AURPackageDataSource{manager: &fakeAURPackageManager{}},
			config: &AURPackageDataSourceModel{
				ID:               types.StringUnknown(),
				Name:             types.StringValue("bad name"),
				IncludeRemote:    types.BoolNull(),
				Installed:        types.BoolUnknown(),
				InstallReason:    types.StringUnknown(),
				InstalledVersion: types.StringUnknown(),
				CandidateVersion: types.StringUnknown(),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			resp := readDataSourceResponseForTest(t, test.dataSource, test.config)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected invalid package name diagnostic")
			}
		})
	}
}

type fakeMacOSAudioDataSourceManager struct {
	devices []MacOSAudioDevice
}

func (m *fakeMacOSAudioDataSourceManager) ListDevices(ctx context.Context) ([]MacOSAudioDevice, error) {
	return m.devices, nil
}

func (m *fakeMacOSAudioDataSourceManager) ReadMultiOutput(ctx context.Context, uid string) (MacOSAudioMultiOutputSpec, bool, error) {
	return MacOSAudioMultiOutputSpec{}, false, nil
}

func (m *fakeMacOSAudioDataSourceManager) WriteMultiOutput(ctx context.Context, spec MacOSAudioMultiOutputSpec) (MacOSAudioMultiOutputSpec, error) {
	return spec, nil
}

func (m *fakeMacOSAudioDataSourceManager) DeleteMultiOutput(ctx context.Context, uid string) error {
	return nil
}

func TestMacOSAudioDeviceDataSourceRead(t *testing.T) {
	t.Parallel()

	dataSource := &MacOSAudioDeviceDataSource{
		manager: &fakeMacOSAudioDataSourceManager{
			devices: []MacOSAudioDevice{
				{
					UID:            "BlackHole2ch_UID",
					Name:           "BlackHole 2ch",
					Manufacturer:   "Existential Audio Inc.",
					InputChannels:  2,
					OutputChannels: 2,
				},
			},
		},
	}
	config := MacOSAudioDeviceDataSourceModel{
		ID:             types.StringUnknown(),
		UID:            types.StringNull(),
		Name:           types.StringValue("BlackHole 2ch"),
		BuiltinOutput:  types.StringNull(),
		Manufacturer:   types.StringUnknown(),
		InputChannels:  types.Int64Unknown(),
		OutputChannels: types.Int64Unknown(),
	}
	var state MacOSAudioDeviceDataSourceModel
	readDataSourceForTest(t, dataSource, &config, &state)

	if state.UID.ValueString() != "BlackHole2ch_UID" || state.OutputChannels.ValueInt64() != 2 {
		t.Fatalf("unexpected audio device state: %#v", state)
	}
}

func readDataSourceForTest(t *testing.T, dataSource datasource.DataSource, config any, state any) {
	t.Helper()

	resp := readDataSourceResponseForTest(t, dataSource, config)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read data source: %v", resp.Diagnostics)
	}
	if diags := resp.State.Get(t.Context(), state); diags.HasError() {
		t.Fatalf("decode data source state: %v", diags)
	}
}

func readDataSourceResponseForTest(t *testing.T, dataSource datasource.DataSource, config any) datasource.ReadResponse {
	t.Helper()

	schema := dataSourceSchemaForTest(t, dataSource)
	tfType := schema.Type().TerraformType(t.Context())
	configState := tfsdk.State{
		Schema: schema,
		Raw:    tftypes.NewValue(tfType, nil),
	}
	if diags := configState.Set(t.Context(), config); diags.HasError() {
		t.Fatalf("encode data source config: %v", diags)
	}
	req := datasource.ReadRequest{
		Config: tfsdk.Config{Schema: schema, Raw: configState.Raw},
	}
	resp := datasource.ReadResponse{
		State: tfsdk.State{
			Schema: schema,
			Raw:    tftypes.NewValue(tfType, nil),
		},
	}
	dataSource.Read(t.Context(), req, &resp)
	return resp
}

func dataSourceSchemaForTest(t *testing.T, dataSource datasource.DataSource) datasourceschema.Schema {
	t.Helper()

	var resp datasource.SchemaResponse
	dataSource.Schema(t.Context(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("data source schema: %v", resp.Diagnostics)
	}
	return resp.Schema
}
