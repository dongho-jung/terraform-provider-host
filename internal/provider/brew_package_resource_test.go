package provider

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type fakeBrewPackageManager struct {
	statuses        map[string]BrewPackageStatus
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

	state, installed, err := resource.refreshState(context.Background(), BrewPackageResourceModel{
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

	state, installed, err := resource.refreshState(context.Background(), BrewPackageResourceModel{
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

func TestBrewPackageResourceSyncInstallsMissingPackage(t *testing.T) {
	t.Parallel()

	manager := &fakeBrewPackageManager{
		statuses: map[string]BrewPackageStatus{},
	}
	resource := &BrewPackageResource{manager: manager}

	err := resource.syncPackage(context.Background(), BrewPackageResourceModel{
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

	err := resource.syncPackage(context.Background(), BrewPackageResourceModel{
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

	err := resource.syncPackage(context.Background(), BrewPackageResourceModel{
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

	err := resource.syncPackage(context.Background(), BrewPackageResourceModel{
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

	err := resource.syncPackage(context.Background(), BrewPackageResourceModel{
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

	err := resource.syncPackage(context.Background(), BrewPackageResourceModel{
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

	err := resource.syncPackage(context.Background(), BrewPackageResourceModel{
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
	if err := manager.RemovePackage(context.Background(), "firefox", brewPackageTypeCask, false, true); err != nil {
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
