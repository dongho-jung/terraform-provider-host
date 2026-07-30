package provider

import (
	"context"
	"reflect"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type fakePackageManager struct {
	statuses map[string]PackageStatus
}

func (m fakePackageManager) PackageStatus(ctx context.Context, name string) (PackageStatus, error) {
	if status, ok := m.statuses[name]; ok {
		return status, nil
	}

	return PackageStatus{Name: name}, nil
}

func (m fakePackageManager) InstallPackages(ctx context.Context, names []string) error {
	return nil
}

func (m fakePackageManager) UpgradePackages(ctx context.Context, names []string) error {
	return nil
}

func (m fakePackageManager) MarkUserPackages(ctx context.Context, names []string) error {
	return nil
}

func (m fakePackageManager) RemovePackages(ctx context.Context, names []string, autoremove bool) error {
	return nil
}

type recordingPackageManager struct {
	statuses map[string]PackageStatus
	upgraded []string
}

func (m *recordingPackageManager) PackageStatus(ctx context.Context, name string) (PackageStatus, error) {
	if status, ok := m.statuses[name]; ok {
		return status, nil
	}

	return PackageStatus{Name: name}, nil
}

func (m *recordingPackageManager) InstallPackages(ctx context.Context, names []string) error {
	return nil
}

func (m *recordingPackageManager) UpgradePackages(ctx context.Context, names []string) error {
	m.upgraded = append(m.upgraded, names...)
	return nil
}

func (m *recordingPackageManager) MarkUserPackages(ctx context.Context, names []string) error {
	return nil
}

func (m *recordingPackageManager) RemovePackages(ctx context.Context, names []string, autoremove bool) error {
	return nil
}

type optionRecordingPackageManager struct {
	fakePackageManager
	includeVersions []bool
}

func (m *optionRecordingPackageManager) PackageStatusWithOptions(ctx context.Context, name string, includeVersions bool) (PackageStatus, error) {
	m.includeVersions = append(m.includeVersions, includeVersions)
	return m.PackageStatus(ctx, name)
}

func TestDNFPackageResourceRefreshStateInstalledUserPackage(t *testing.T) {
	t.Parallel()

	resource := &DNFPackageResource{
		manager: fakePackageManager{
			statuses: map[string]PackageStatus{
				"git": {
					Name:             "git",
					Installed:        true,
					ReasonUser:       true,
					InstalledVersion: "2.50.1-1.fc44",
					CandidateVersion: "2.50.1-1.fc44",
					UpgradeVersion:   "",
				},
			},
		},
	}

	state, installed, err := resource.refreshState(t.Context(), DNFPackageResourceModel{
		Name:       types.StringValue("git"),
		Autoremove: types.BoolValue(true),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if state.ID.ValueString() != "git" {
		t.Fatalf("expected id git, got %q", state.ID.ValueString())
	}

	if !installed {
		t.Fatal("expected installed package")
	}

	if state.Version.ValueString() != versionLatest {
		t.Fatalf("expected version policy latest, got %q", state.Version.ValueString())
	}
	if !state.IgnoreVersion.ValueBool() {
		t.Fatal("expected ignore_version to default to true")
	}

	if state.InstalledVersion.ValueString() != "2.50.1-1.fc44" {
		t.Fatalf("expected installed version, got %q", state.InstalledVersion.ValueString())
	}

	if !state.CandidateVersion.IsNull() {
		t.Fatalf("expected ignored candidate version to be null, got %#v", state.CandidateVersion)
	}
	if state.InstallReason.ValueString() != dnfPackageInstallReasonUser {
		t.Fatalf("install reason %q, want user", state.InstallReason.ValueString())
	}
}

func TestDNFPackageResourceInstalledDependencyPlansAndAppliesUserReason(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	manager := &convergingPackageManager{
		status: PackageStatus{
			Name:             "glib2",
			Installed:        true,
			InstallReason:    dnfPackageInstallReasonDependency,
			InstalledVersion: "2.84.4-1.fc44",
		},
	}
	r := &DNFPackageResource{manager: manager}

	var schemaResp frameworkresource.SchemaResponse
	r.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("resource schema: %v", schemaResp.Diagnostics)
	}

	stateModel, installed, err := r.refreshState(ctx, DNFPackageResourceModel{
		Name: types.StringValue("glib2"),
	})
	if err != nil {
		t.Fatalf("refresh dependency state: %s", err)
	}
	if !installed {
		t.Fatal("expected glib2 to be installed")
	}
	if stateModel.InstallReason.ValueString() != dnfPackageInstallReasonDependency {
		t.Fatalf("observed install reason %q, want dependency", stateModel.InstallReason.ValueString())
	}

	state := tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	if diags := state.Set(ctx, &stateModel); diags.HasError() {
		t.Fatalf("encode state: %v", diags)
	}

	planModel := stateModel
	planModel.InstallReason = types.StringValue(dnfPackageInstallReasonUser)
	plan := tfsdk.Plan{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	if diags := plan.Set(ctx, &planModel); diags.HasError() {
		t.Fatalf("encode plan: %v", diags)
	}

	modifyResp := frameworkresource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, frameworkresource.ModifyPlanRequest{State: state, Plan: plan}, &modifyResp)
	if modifyResp.Diagnostics.HasError() {
		t.Fatalf("modify plan: %v", modifyResp.Diagnostics)
	}
	var modifiedPlan DNFPackageResourceModel
	if diags := modifyResp.Plan.Get(ctx, &modifiedPlan); diags.HasError() {
		t.Fatalf("decode modified plan: %v", diags)
	}
	if modifiedPlan.InstallReason.ValueString() != dnfPackageInstallReasonUser {
		t.Fatalf("planned install reason %q, want user", modifiedPlan.InstallReason.ValueString())
	}
	if stateModel.InstallReason.Equal(modifiedPlan.InstallReason) {
		t.Fatal("expected observed dependency reason to differ from desired user reason")
	}
	if manager.statusCalls != 1 {
		t.Fatalf("ModifyPlan performed a live package lookup; status calls = %d, want 1 from refresh only", manager.statusCalls)
	}

	updateResp := frameworkresource.UpdateResponse{
		State: tfsdk.State{Schema: schemaResp.Schema, Raw: modifyResp.Plan.Raw},
	}
	r.Update(ctx, frameworkresource.UpdateRequest{State: state, Plan: modifyResp.Plan}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update: %v", updateResp.Diagnostics)
	}
	if want := []string{"glib2"}; !reflect.DeepEqual(manager.marked, want) {
		t.Fatalf("marked %#v, want %#v", manager.marked, want)
	}

	var appliedState DNFPackageResourceModel
	if diags := updateResp.State.Get(ctx, &appliedState); diags.HasError() {
		t.Fatalf("decode applied state: %v", diags)
	}
	if appliedState.InstallReason.ValueString() != dnfPackageInstallReasonUser {
		t.Fatalf("applied install reason %q, want user", appliedState.InstallReason.ValueString())
	}
}

func TestDNFPackageResourceIgnoredVersionPlanRespectsPriorState(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	manager := &convergingPackageManager{
		status: PackageStatus{
			Name:             "git",
			Installed:        true,
			ReasonUser:       true,
			InstallReason:    dnfPackageInstallReasonUser,
			InstalledVersion: "2.52.0-1.fc44",
		},
	}
	r := &DNFPackageResource{manager: manager}

	var schemaResp frameworkresource.SchemaResponse
	r.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("resource schema: %v", schemaResp.Diagnostics)
	}

	stateModel := DNFPackageResourceModel{
		ID:               types.StringValue("git"),
		Name:             types.StringValue("git"),
		Version:          types.StringValue(versionLatest),
		IgnoreVersion:    types.BoolValue(true),
		Autoremove:       types.BoolValue(true),
		InstallReason:    types.StringValue(dnfPackageInstallReasonUser),
		InstalledVersion: types.StringValue("2.51.0-1.fc44"),
		CandidateVersion: types.StringNull(),
	}
	state := tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	if diags := state.Set(ctx, &stateModel); diags.HasError() {
		t.Fatalf("encode state: %v", diags)
	}
	plan := tfsdk.Plan{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	if diags := plan.Set(ctx, &stateModel); diags.HasError() {
		t.Fatalf("encode plan: %v", diags)
	}

	modifyResp := frameworkresource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, frameworkresource.ModifyPlanRequest{State: state, Plan: plan}, &modifyResp)
	if modifyResp.Diagnostics.HasError() {
		t.Fatalf("modify plan: %v", modifyResp.Diagnostics)
	}
	var got DNFPackageResourceModel
	if diags := modifyResp.Plan.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode modified plan: %v", diags)
	}
	if got.InstalledVersion.ValueString() != "2.51.0-1.fc44" {
		t.Fatalf("installed version %q, want prior state version", got.InstalledVersion.ValueString())
	}
	if !got.CandidateVersion.IsNull() {
		t.Fatalf("candidate version should remain null, got %#v", got.CandidateVersion)
	}
	if manager.statusCalls != 0 {
		t.Fatalf("ModifyPlan performed %d live package lookups, want none", manager.statusCalls)
	}
}

func TestDNFPackageResourceRefreshStateMissingPackage(t *testing.T) {
	t.Parallel()

	resource := &DNFPackageResource{
		manager: fakePackageManager{
			statuses: map[string]PackageStatus{},
		},
	}

	_, installed, err := resource.refreshState(t.Context(), DNFPackageResourceModel{
		Name:       types.StringValue("git"),
		Autoremove: types.BoolValue(true),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if installed {
		t.Fatal("expected missing package")
	}
}

func TestDNFPackageResourceSkipsVersionQueriesWhenIgnored(t *testing.T) {
	t.Parallel()

	manager := &optionRecordingPackageManager{
		fakePackageManager: fakePackageManager{
			statuses: map[string]PackageStatus{
				"git": {
					Name:             "git",
					Installed:        true,
					ReasonUser:       true,
					InstallReason:    dnfPackageInstallReasonUser,
					InstalledVersion: "2.51.0-1.fc44",
					CandidateVersion: "must-not-be-used",
				},
			},
		},
	}
	resource := &DNFPackageResource{manager: manager}

	state, installed, err := resource.refreshState(t.Context(), DNFPackageResourceModel{
		Name: types.StringValue("git"),
	})
	if err != nil {
		t.Fatalf("refresh package: %s", err)
	}
	if !installed {
		t.Fatal("expected installed package")
	}
	if !state.CandidateVersion.IsNull() {
		t.Fatalf("candidate version should be null, got %#v", state.CandidateVersion)
	}
	if want := []bool{false}; !reflect.DeepEqual(manager.includeVersions, want) {
		t.Fatalf("includeVersions calls %#v, want %#v", manager.includeVersions, want)
	}
}

func TestDNFPackageResourceSyncIgnoresOutdatedPackageByDefault(t *testing.T) {
	t.Parallel()

	manager := &recordingPackageManager{
		statuses: map[string]PackageStatus{
			"git": {
				Name:             "git",
				Installed:        true,
				ReasonUser:       true,
				InstalledVersion: "2.50.1-1.fc44",
				CandidateVersion: "2.51.0-1.fc44",
				UpgradeVersion:   "2.51.0-1.fc44",
			},
		},
	}
	resource := &DNFPackageResource{manager: manager}

	err := resource.syncPackage(t.Context(), DNFPackageResourceModel{
		Name:    types.StringValue("git"),
		Version: types.StringValue(versionLatest),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(manager.upgraded) != 0 {
		t.Fatalf("expected no upgrade by default, got %#v", manager.upgraded)
	}
}

func TestDNFPackageResourceSyncUpgradesWhenVersionIsNotIgnored(t *testing.T) {
	t.Parallel()

	manager := &recordingPackageManager{
		statuses: map[string]PackageStatus{
			"git": {
				Name:             "git",
				Installed:        true,
				ReasonUser:       true,
				InstalledVersion: "2.50.1-1.fc44",
				CandidateVersion: "2.51.0-1.fc44",
				UpgradeVersion:   "2.51.0-1.fc44",
			},
		},
	}
	resource := &DNFPackageResource{manager: manager}

	err := resource.syncPackage(t.Context(), DNFPackageResourceModel{
		Name:          types.StringValue("git"),
		Version:       types.StringValue(versionLatest),
		IgnoreVersion: types.BoolValue(false),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	want := []string{"git"}
	if !reflect.DeepEqual(manager.upgraded, want) {
		t.Fatalf("upgraded %#v, want %#v", manager.upgraded, want)
	}
}

func TestValidatePackageName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"git", "nodejs22", "NetworkManager-openvpn-gnome", "foo@1.2_3+meta"} {
		if err := validatePackageName(name); err != nil {
			t.Fatalf("expected %q to be valid: %s", name, err)
		}
	}

	for _, name := range []string{
		"",
		"-invalid",
		".invalid",
		" git",
		"git ",
		"git docs",
		"git\tdocs",
		"git\ndocs",
		"git\x00docs",
		"owner/package",
		"café",
	} {
		if err := validatePackageName(name); err == nil {
			t.Fatalf("expected %q to be invalid", name)
		}
	}
}

func TestParsePackageNames(t *testing.T) {
	t.Parallel()

	got := parsePackageNames([]byte("git\n\nbash\ngit\n"))
	want := []string{"bash", "git"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
