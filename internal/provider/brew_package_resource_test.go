package provider

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type fakeBrewPackageManager struct {
	statuses        map[string]BrewPackageStatus
	statusCalls     int
	taps            map[string]bool
	tapped          []string
	installed       []string
	upgraded        []string
	markedOnRequest []string
	removed         []string
}

func (m *fakeBrewPackageManager) TapInstalled(ctx context.Context, name string) (bool, error) {
	return m.taps[name], nil
}

func (m *fakeBrewPackageManager) Tap(ctx context.Context, name string) error {
	m.tapped = append(m.tapped, name)
	if m.taps == nil {
		m.taps = map[string]bool{}
	}
	m.taps[name] = true

	return nil
}

func (m *fakeBrewPackageManager) PackageStatus(ctx context.Context, name string, packageType string) (BrewPackageStatus, error) {
	m.statusCalls++
	if status, ok := m.statuses[brewPackageID(packageType, name)]; ok {
		return status, nil
	}

	return BrewPackageStatus{
		Name:        name,
		PackageType: packageType,
	}, nil
}

func (m *fakeBrewPackageManager) InstallPackage(ctx context.Context, name string, packageType string) error {
	m.installed = append(m.installed, brewPackageID(packageType, name))
	return nil
}

func (m *fakeBrewPackageManager) UpgradePackage(ctx context.Context, name string, packageType string) error {
	m.upgraded = append(m.upgraded, brewPackageID(packageType, name))
	return nil
}

func (m *fakeBrewPackageManager) MarkPackageOnRequest(ctx context.Context, name string) error {
	m.markedOnRequest = append(m.markedOnRequest, name)
	for id, status := range m.statuses {
		if status.PackageType == brewPackageTypeFormula && (status.Name == name || id == brewPackageID(brewPackageTypeFormula, name)) {
			status.InstalledOnRequest = true
			m.statuses[id] = status
		}
	}
	return nil
}

func (m *fakeBrewPackageManager) RemovePackage(ctx context.Context, name string, packageType string, autoremove bool, zap bool) error {
	m.removed = append(m.removed, brewPackageID(packageType, name))
	return nil
}

type privilegeReportingBrewPackageManager struct {
	fakeBrewPackageManager
}

func (m *privilegeReportingBrewPackageManager) NeedsPrivilegeEscalation() bool {
	return true
}

func TestBrewPackageResourceRefreshStateInstalledFormula(t *testing.T) {
	t.Parallel()

	resource := &BrewPackageResource{
		manager: &fakeBrewPackageManager{
			statuses: map[string]BrewPackageStatus{
				"formula:bat": {
					Name:               "bat",
					PackageType:        brewPackageTypeFormula,
					Installed:          true,
					InstalledVersion:   "0.26.1",
					CandidateVersion:   "0.26.1",
					InstalledOnRequest: true,
				},
			},
		},
	}

	state, installed, err := resource.refreshState(t.Context(), BrewPackageResourceModel{
		Name:          types.StringValue("bat"),
		PackageType:   types.StringValue(brewPackageTypeFormula),
		Version:       types.StringValue(versionLatest),
		IgnoreVersion: types.BoolValue(false),
		Autoremove:    types.BoolValue(true),
		Zap:           types.BoolValue(false),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if !installed {
		t.Fatal("expected installed formula")
	}
	if state.ID.ValueString() != "formula:bat" {
		t.Fatalf("expected id formula:bat, got %q", state.ID.ValueString())
	}
	if state.InstalledVersion.ValueString() != "0.26.1" {
		t.Fatalf("expected installed version 0.26.1, got %q", state.InstalledVersion.ValueString())
	}
	if state.CandidateVersion.ValueString() != "0.26.1" {
		t.Fatalf("expected candidate version 0.26.1, got %q", state.CandidateVersion.ValueString())
	}
}

func TestBrewPackageResourceRefreshStateDefaultsToIgnoreVersion(t *testing.T) {
	t.Parallel()

	resource := &BrewPackageResource{
		manager: &fakeBrewPackageManager{
			statuses: map[string]BrewPackageStatus{
				"formula:bat": {
					Name:             "bat",
					PackageType:      brewPackageTypeFormula,
					Installed:        true,
					InstalledVersion: "0.25.0",
					CandidateVersion: "0.26.1",
				},
			},
		},
	}

	state, installed, err := resource.refreshState(t.Context(), BrewPackageResourceModel{
		Name:        types.StringValue("bat"),
		PackageType: types.StringValue(brewPackageTypeFormula),
		Version:     types.StringValue(versionLatest),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if !installed {
		t.Fatal("expected installed formula")
	}
	if !state.IgnoreVersion.ValueBool() {
		t.Fatal("expected ignore_version to default to true")
	}
	if !state.CandidateVersion.IsNull() {
		t.Fatalf("expected ignored candidate version to be null, got %#v", state.CandidateVersion)
	}
}

func TestBrewPackageResourceRefreshStateCaskAppPaths(t *testing.T) {
	t.Parallel()

	resource := &BrewPackageResource{
		manager: &fakeBrewPackageManager{
			statuses: map[string]BrewPackageStatus{
				"cask:hammerspoon": {
					Name:             "hammerspoon",
					PackageType:      brewPackageTypeCask,
					Installed:        true,
					InstalledVersion: "1.1.1",
					CandidateVersion: "1.1.1",
					AppPaths:         []string{"/Applications/Hammerspoon.app"},
				},
			},
		},
	}

	state, installed, err := resource.refreshState(t.Context(), BrewPackageResourceModel{
		Name:        types.StringValue("hammerspoon"),
		PackageType: types.StringValue(brewPackageTypeCask),
		Version:     types.StringValue(versionLatest),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if !installed {
		t.Fatal("expected installed cask")
	}
	if state.AppPath.ValueString() != "/Applications/Hammerspoon.app" {
		t.Fatalf("expected app_path, got %#v", state.AppPath)
	}
	var appPaths []string
	diags := state.AppPaths.ElementsAs(t.Context(), &appPaths, false)
	if diags.HasError() {
		t.Fatalf("app_paths diagnostics: %s", diagnosticsError(diags))
	}
	if !reflect.DeepEqual(appPaths, []string{"/Applications/Hammerspoon.app"}) {
		t.Fatalf("app_paths got %#v", appPaths)
	}
}

func TestBrewPackageResourceRefreshStateCaskMultipleAppPathsLeavesAppPathNull(t *testing.T) {
	t.Parallel()

	resource := &BrewPackageResource{
		manager: &fakeBrewPackageManager{
			statuses: map[string]BrewPackageStatus{
				"cask:example": {
					Name:             "example",
					PackageType:      brewPackageTypeCask,
					Installed:        true,
					InstalledVersion: "1.0.0",
					CandidateVersion: "1.0.0",
					AppPaths: []string{
						"/Applications/Example.app",
						"/Applications/Example Helper.app",
					},
				},
			},
		},
	}

	state, installed, err := resource.refreshState(t.Context(), BrewPackageResourceModel{
		Name:        types.StringValue("example"),
		PackageType: types.StringValue(brewPackageTypeCask),
		Version:     types.StringValue(versionLatest),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if !installed {
		t.Fatal("expected installed cask")
	}
	if !state.AppPath.IsNull() {
		t.Fatalf("expected app_path to be null for multiple apps, got %#v", state.AppPath)
	}
}

func TestBrewPackageResourceSyncInstallsMissingPackage(t *testing.T) {
	t.Parallel()

	manager := &fakeBrewPackageManager{
		statuses: map[string]BrewPackageStatus{},
	}
	resource := &BrewPackageResource{manager: manager}

	err := resource.syncPackage(t.Context(), BrewPackageResourceModel{
		Name:        types.StringValue("bat"),
		PackageType: types.StringValue(brewPackageTypeFormula),
		Version:     types.StringValue(versionLatest),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	want := []string{"formula:bat"}
	if !reflect.DeepEqual(manager.installed, want) {
		t.Fatalf("installed %#v, want %#v", manager.installed, want)
	}
	if len(manager.markedOnRequest) != 0 {
		t.Fatalf("new install should not need a second mark-on-request command, got %#v", manager.markedOnRequest)
	}
}

func TestBrewPackageResourceSyncTapsBeforeInstallingPackage(t *testing.T) {
	t.Parallel()

	manager := &fakeBrewPackageManager{
		statuses: map[string]BrewPackageStatus{},
		taps:     map[string]bool{},
	}
	resource := &BrewPackageResource{manager: manager}

	err := resource.syncPackage(t.Context(), BrewPackageResourceModel{
		Name:        types.StringValue("terraform"),
		Tap:         types.StringValue("hashicorp/tap"),
		PackageType: types.StringValue(brewPackageTypeFormula),
		Version:     types.StringValue(versionLatest),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if !reflect.DeepEqual(manager.tapped, []string{"hashicorp/tap"}) {
		t.Fatalf("tapped %#v, want hashicorp/tap", manager.tapped)
	}
	wantInstalled := []string{"formula:hashicorp/tap/terraform"}
	if !reflect.DeepEqual(manager.installed, wantInstalled) {
		t.Fatalf("installed %#v, want %#v", manager.installed, wantInstalled)
	}
}

func TestBrewPackageResourceSyncUpgradesOutdatedCask(t *testing.T) {
	t.Parallel()

	manager := &fakeBrewPackageManager{
		statuses: map[string]BrewPackageStatus{
			"cask:docker-desktop": {
				Name:             "docker-desktop",
				PackageType:      brewPackageTypeCask,
				Installed:        true,
				InstalledVersion: "4.71.0",
				CandidateVersion: "4.79.0",
				UpgradeVersion:   "4.79.0",
				AutoUpdates:      true,
			},
		},
	}
	resource := &BrewPackageResource{manager: manager}

	err := resource.syncPackage(t.Context(), BrewPackageResourceModel{
		Name:          types.StringValue("docker-desktop"),
		PackageType:   types.StringValue(brewPackageTypeCask),
		Version:       types.StringValue(versionLatest),
		IgnoreVersion: types.BoolValue(false),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	want := []string{"cask:docker-desktop"}
	if !reflect.DeepEqual(manager.upgraded, want) {
		t.Fatalf("upgraded %#v, want %#v", manager.upgraded, want)
	}
}

func TestBrewPackageResourceSyncIgnoresOutdatedPackageByDefault(t *testing.T) {
	t.Parallel()

	manager := &fakeBrewPackageManager{
		statuses: map[string]BrewPackageStatus{
			"formula:bat": {
				Name:             "bat",
				PackageType:      brewPackageTypeFormula,
				Installed:        true,
				InstalledVersion: "0.25.0",
				CandidateVersion: "0.26.1",
				UpgradeVersion:   "0.26.1",
			},
		},
	}
	resource := &BrewPackageResource{manager: manager}

	err := resource.syncPackage(t.Context(), BrewPackageResourceModel{
		Name:        types.StringValue("bat"),
		PackageType: types.StringValue(brewPackageTypeFormula),
		Version:     types.StringValue(versionLatest),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(manager.upgraded) != 0 {
		t.Fatalf("expected no upgrade by default, got %#v", manager.upgraded)
	}
}

func TestBrewPackageResourceExactVersionRejectsUnavailableVersion(t *testing.T) {
	t.Parallel()

	manager := &fakeBrewPackageManager{
		statuses: map[string]BrewPackageStatus{
			"formula:bat": {
				Name:             "bat",
				PackageType:      brewPackageTypeFormula,
				Installed:        true,
				InstalledVersion: "0.25.0",
				CandidateVersion: "0.26.1",
				UpgradeVersion:   "0.26.1",
			},
		},
	}
	resource := &BrewPackageResource{manager: manager}

	err := resource.syncPackage(t.Context(), BrewPackageResourceModel{
		Name:          types.StringValue("bat"),
		PackageType:   types.StringValue(brewPackageTypeFormula),
		Version:       types.StringValue("0.24.0"),
		IgnoreVersion: types.BoolValue(false),
	})
	if err == nil {
		t.Fatal("expected unavailable exact version error")
	}
	if !strings.Contains(err.Error(), "requested version") {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestBrewPackageResourceExactVersionCanUpgradeToCandidate(t *testing.T) {
	t.Parallel()

	manager := &fakeBrewPackageManager{
		statuses: map[string]BrewPackageStatus{
			"formula:bat": {
				Name:             "bat",
				PackageType:      brewPackageTypeFormula,
				Installed:        true,
				InstalledVersion: "0.25.0",
				CandidateVersion: "0.26.1",
				UpgradeVersion:   "0.26.1",
			},
		},
	}
	resource := &BrewPackageResource{manager: manager}

	err := resource.syncPackage(t.Context(), BrewPackageResourceModel{
		Name:          types.StringValue("bat"),
		PackageType:   types.StringValue(brewPackageTypeFormula),
		Version:       types.StringValue("0.26.1"),
		IgnoreVersion: types.BoolValue(false),
	})
	if err == nil {
		t.Fatal("expected fake manager post-upgrade version mismatch")
	}

	want := []string{"formula:bat"}
	if !reflect.DeepEqual(manager.upgraded, want) {
		t.Fatalf("upgraded %#v, want %#v", manager.upgraded, want)
	}
}

func TestMarkBrewVersionStateUnknown(t *testing.T) {
	t.Parallel()

	model := BrewPackageResourceModel{
		InstalledVersion: types.StringValue("1.17377.1"),
		CandidateVersion: types.StringValue("1.17377.1"),
	}

	markBrewVersionStateUnknown(&model)

	if !model.InstalledVersion.IsUnknown() {
		t.Fatalf("installed version should be unknown, got %#v", model.InstalledVersion)
	}
	if !model.CandidateVersion.IsUnknown() {
		t.Fatalf("candidate version should be unknown, got %#v", model.CandidateVersion)
	}
}

func TestBrewPackageIgnoresVersionKeepsExactVersionEnforced(t *testing.T) {
	t.Parallel()

	if brewPackageIgnoresVersion(BrewPackageResourceModel{
		Version:       types.StringValue("0.26.1"),
		IgnoreVersion: types.BoolValue(true),
	}) {
		t.Fatal("exact version should be enforced even when ignore_version is true")
	}
}

func TestBrewPackageResourceIgnoredVersionPlanRespectsPriorState(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	manager := &fakeBrewPackageManager{
		statuses: map[string]BrewPackageStatus{
			"formula:bat": {
				Name:             "bat",
				PackageType:      brewPackageTypeFormula,
				Installed:        true,
				InstalledVersion: "0.27.0",
			},
		},
	}
	r := &BrewPackageResource{manager: manager}

	var schemaResp frameworkresource.SchemaResponse
	r.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("resource schema: %v", schemaResp.Diagnostics)
	}

	stateModel := BrewPackageResourceModel{
		ID:               types.StringValue("formula:bat"),
		Name:             types.StringValue("bat"),
		Tap:              types.StringNull(),
		PackageType:      types.StringValue(brewPackageTypeFormula),
		Version:          types.StringValue(versionLatest),
		IgnoreVersion:    types.BoolValue(true),
		Autoremove:       types.BoolValue(true),
		Zap:              types.BoolValue(false),
		InstallReason:    types.StringValue(brewPackageInstallReasonOnRequest),
		InstalledVersion: types.StringValue("0.26.1"),
		CandidateVersion: types.StringNull(),
		Pinned:           types.BoolValue(false),
		AppPath:          types.StringNull(),
		AppPaths:         types.ListValueMust(types.StringType, nil),
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
	var got BrewPackageResourceModel
	if diags := modifyResp.Plan.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode modified plan: %v", diags)
	}
	if got.InstalledVersion.ValueString() != "0.26.1" {
		t.Fatalf("installed version %q, want prior state version", got.InstalledVersion.ValueString())
	}
	if !got.CandidateVersion.IsNull() {
		t.Fatalf("candidate version should remain null, got %#v", got.CandidateVersion)
	}
	if manager.statusCalls != 0 {
		t.Fatalf("ModifyPlan performed %d live package lookups, want none", manager.statusCalls)
	}
}

func TestBrewPackageResourceDependencyFormulaPlansAndAppliesOnRequestReason(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	manager := &fakeBrewPackageManager{
		statuses: map[string]BrewPackageStatus{
			"formula:libgit2": {
				Name:               "libgit2",
				PackageType:        brewPackageTypeFormula,
				Installed:          true,
				InstalledVersion:   "1.9.4",
				InstalledOnRequest: false,
			},
		},
	}
	r := &BrewPackageResource{manager: manager}

	var schemaResp frameworkresource.SchemaResponse
	r.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("resource schema: %v", schemaResp.Diagnostics)
	}

	stateModel, installed, err := r.refreshState(ctx, BrewPackageResourceModel{
		Name:        types.StringValue("libgit2"),
		PackageType: types.StringValue(brewPackageTypeFormula),
		Version:     types.StringValue(versionLatest),
	})
	if err != nil {
		t.Fatalf("refresh dependency state: %s", err)
	}
	if !installed {
		t.Fatal("expected libgit2 to be installed")
	}
	if stateModel.InstallReason.ValueString() != brewPackageInstallReasonDependency {
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
	planModel.InstallReason = types.StringValue(brewPackageInstallReasonOnRequest)
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
	var modifiedPlan BrewPackageResourceModel
	if diags := modifyResp.Plan.Get(ctx, &modifiedPlan); diags.HasError() {
		t.Fatalf("decode modified plan: %v", diags)
	}
	if modifiedPlan.InstallReason.ValueString() != brewPackageInstallReasonOnRequest {
		t.Fatalf("planned install reason %q, want on_request", modifiedPlan.InstallReason.ValueString())
	}
	if stateModel.InstallReason.Equal(modifiedPlan.InstallReason) {
		t.Fatal("expected observed dependency reason to differ from desired on_request reason")
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
	if want := []string{"libgit2"}; !reflect.DeepEqual(manager.markedOnRequest, want) {
		t.Fatalf("marked on request %#v, want %#v", manager.markedOnRequest, want)
	}

	var appliedState BrewPackageResourceModel
	if diags := updateResp.State.Get(ctx, &appliedState); diags.HasError() {
		t.Fatalf("decode applied state: %v", diags)
	}
	if appliedState.InstallReason.ValueString() != brewPackageInstallReasonOnRequest {
		t.Fatalf("applied install reason %q, want on_request", appliedState.InstallReason.ValueString())
	}
}

func TestBrewPackageCommandNameSupportsQualifiedPackageName(t *testing.T) {
	t.Parallel()

	got := brewPackageCommandName(BrewPackageResourceModel{
		Name: types.StringValue("hashicorp/tap/terraform"),
	})

	if got != "hashicorp/tap/terraform" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateBrewResourcePlanRejectsMismatchedTap(t *testing.T) {
	t.Parallel()

	err := validateBrewResourcePlan(BrewPackageResourceModel{
		Name:        types.StringValue("hashicorp/tap/terraform"),
		Tap:         types.StringValue("homebrew/core"),
		PackageType: types.StringValue(brewPackageTypeFormula),
		Version:     types.StringValue(versionLatest),
	})
	if err == nil {
		t.Fatal("expected mismatched tap error")
	}
}

func TestBrewPackageResourceSyncMarksDependencyFormulaOnRequest(t *testing.T) {
	t.Parallel()

	manager := &fakeBrewPackageManager{
		statuses: map[string]BrewPackageStatus{
			"formula:libgit2": {
				Name:               "libgit2",
				PackageType:        brewPackageTypeFormula,
				Installed:          true,
				InstalledVersion:   "1.9.4",
				CandidateVersion:   "1.9.4",
				InstalledOnRequest: false,
			},
		},
	}
	resource := &BrewPackageResource{manager: manager}

	err := resource.syncPackage(t.Context(), BrewPackageResourceModel{
		Name:        types.StringValue("libgit2"),
		PackageType: types.StringValue(brewPackageTypeFormula),
		Version:     types.StringValue(versionLatest),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	want := []string{"libgit2"}
	if !reflect.DeepEqual(manager.markedOnRequest, want) {
		t.Fatalf("marked %#v, want %#v", manager.markedOnRequest, want)
	}
}

func TestParseBrewPackageImportID(t *testing.T) {
	t.Parallel()

	packageType, name, err := parseBrewPackageImportID("cask:firefox")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if packageType != brewPackageTypeCask || name != "firefox" {
		t.Fatalf("got %q %q", packageType, name)
	}

	packageType, name, err = parseBrewPackageImportID("bat")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if packageType != brewPackageTypeFormula || name != "bat" {
		t.Fatalf("got %q %q", packageType, name)
	}
}

func TestValidateBrewPackageType(t *testing.T) {
	t.Parallel()

	for _, packageType := range []string{brewPackageTypeFormula, brewPackageTypeCask} {
		if err := validateBrewPackageType(packageType); err != nil {
			t.Fatalf("expected %q to be valid: %s", packageType, err)
		}
	}

	if err := validateBrewPackageType("tap"); err == nil {
		t.Fatal("expected invalid package type")
	}
}

func TestFakeBrewPackageManagerRecordsRemove(t *testing.T) {
	t.Parallel()

	manager := &fakeBrewPackageManager{}
	if err := manager.RemovePackage(t.Context(), "firefox", brewPackageTypeCask, false, true); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	want := []string{"cask:firefox"}
	if !reflect.DeepEqual(manager.removed, want) {
		t.Fatalf("removed %#v, want %#v", manager.removed, want)
	}
}

func TestBrewPackageResourceCaskPlanAddsWarningWithoutAuthenticating(t *testing.T) {
	sudoPlanWarningOnce = sync.Once{}

	resource := &BrewPackageResource{
		manager: &privilegeReportingBrewPackageManager{},
	}

	var diags diag.Diagnostics
	resource.addCaskPrivilegeWarning(&diags, BrewPackageResourceModel{
		Name:        types.StringValue("firefox"),
		PackageType: types.StringValue(brewPackageTypeCask),
	})

	if diags.HasError() {
		t.Fatalf("expected warning only, got diagnostics: %s", diagnosticsError(diags))
	}
	if len(diags.Warnings()) != 1 {
		t.Fatalf("warnings got %d, want 1", len(diags.Warnings()))
	}
}
