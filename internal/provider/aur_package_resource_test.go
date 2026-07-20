package provider

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type fakeAURPackageManager struct {
	statuses      map[string]PackageStatus
	remoteLookups []bool
	statusErr     error
}

func (m *fakeAURPackageManager) PackageStatus(ctx context.Context, name string, includeRemote bool) (PackageStatus, error) {
	m.remoteLookups = append(m.remoteLookups, includeRemote)
	return m.statuses[name], m.statusErr
}

func TestAURPackageFreshPlanDefersRemoteLookupUntilHelperExists(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	manager := &fakeAURPackageManager{
		statuses: map[string]PackageStatus{
			"wl-kbptr": {Name: "wl-kbptr"},
		},
		statusErr: fmt.Errorf("%w: helper will be bootstrapped during apply", errAURHelperUnavailable),
	}
	r := &AURPackageResource{manager: manager}
	var schemaResp frameworkresource.SchemaResponse
	r.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("resource schema: %v", schemaResp.Diagnostics)
	}
	model := AURPackageResourceModel{
		Name:             types.StringValue("wl-kbptr"),
		Version:          types.StringValue(versionLatest),
		IgnoreVersion:    types.BoolValue(false),
		Autoremove:       types.BoolValue(false),
		InstallReason:    types.StringValue(packageInstallReasonExplicit),
		InstalledVersion: types.StringUnknown(),
		CandidateVersion: types.StringUnknown(),
	}
	plan := tfsdk.Plan{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatalf("encode plan: %v", diags)
	}
	state := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)}
	resp := frameworkresource.ModifyPlanResponse{Plan: plan}

	r.ModifyPlan(ctx, frameworkresource.ModifyPlanRequest{State: state, Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("fresh plan must tolerate missing helper: %v", resp.Diagnostics)
	}
	if resp.Diagnostics.WarningsCount() == 0 {
		t.Fatal("expected deferred remote lookup warning")
	}
	var got AURPackageResourceModel
	if diags := resp.Plan.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode plan: %v", diags)
	}
	if !got.CandidateVersion.IsUnknown() {
		t.Fatalf("remote candidate should remain unknown, got %#v", got.CandidateVersion)
	}
	if !got.InstalledVersion.IsUnknown() {
		t.Fatalf("missing package installed version should be unknown, got %#v", got.InstalledVersion)
	}

	err := r.syncPackage(ctx, model)
	if !errors.Is(err, errAURHelperUnavailable) {
		t.Fatalf("apply must still require helper, got %v", err)
	}
}

func (m *fakeAURPackageManager) InstallPackages(ctx context.Context, names []string) error {
	return nil
}

func (m *fakeAURPackageManager) UpgradePackages(ctx context.Context, names []string) error {
	return nil
}

func (m *fakeAURPackageManager) MarkUserPackages(ctx context.Context, names []string) error {
	return nil
}

func (m *fakeAURPackageManager) RemovePackages(ctx context.Context, names []string, autoremove bool) error {
	return nil
}

type recordingAURPackageManager struct {
	fakeAURPackageManager

	installed []string
	upgraded  []string
	marked    []string
}

func (m *recordingAURPackageManager) InstallPackages(ctx context.Context, names []string) error {
	m.installed = append(m.installed, names...)
	return nil
}

func (m *recordingAURPackageManager) UpgradePackages(ctx context.Context, names []string) error {
	m.upgraded = append(m.upgraded, names...)
	return nil
}

func (m *recordingAURPackageManager) MarkUserPackages(ctx context.Context, names []string) error {
	m.marked = append(m.marked, names...)
	return nil
}

func TestAURPackageResourceRefreshStateSkipsRemoteLookupWhenVersionIgnored(t *testing.T) {
	t.Parallel()

	manager := &fakeAURPackageManager{
		statuses: map[string]PackageStatus{
			"wl-kbptr": {
				Name:             "wl-kbptr",
				Installed:        true,
				ReasonUser:       true,
				InstalledVersion: "0.4.1-2",
			},
		},
	}
	resource := &AURPackageResource{manager: manager}

	state, installed, err := resource.refreshState(t.Context(), AURPackageResourceModel{
		Name: types.StringValue("wl-kbptr"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !installed {
		t.Fatal("expected installed package")
	}
	if state.ID.ValueString() != "wl-kbptr" {
		t.Fatalf("expected id wl-kbptr, got %q", state.ID.ValueString())
	}
	if !state.IgnoreVersion.ValueBool() {
		t.Fatal("expected ignore_version to default to true")
	}
	if state.InstalledVersion.ValueString() != "0.4.1-2" {
		t.Fatalf("expected installed version, got %q", state.InstalledVersion.ValueString())
	}
	if state.InstallReason.ValueString() != packageInstallReasonExplicit {
		t.Fatalf("expected explicit install reason, got %q", state.InstallReason.ValueString())
	}
	if !state.CandidateVersion.IsNull() {
		t.Fatalf("expected ignored candidate version to be null, got %#v", state.CandidateVersion)
	}
	if want := []bool{false}; !reflect.DeepEqual(manager.remoteLookups, want) {
		t.Fatalf("remote lookups %#v, want %#v", manager.remoteLookups, want)
	}
}

func TestAURPackageResourceRefreshAndSyncDependencyInstallReason(t *testing.T) {
	t.Parallel()

	manager := &recordingAURPackageManager{
		fakeAURPackageManager: fakeAURPackageManager{
			statuses: map[string]PackageStatus{
				"playerctl": {
					Name:             "playerctl",
					Installed:        true,
					ReasonUser:       false,
					InstalledVersion: "2.4.1-4",
				},
			},
		},
	}
	r := &AURPackageResource{manager: manager}

	state, installed, err := r.refreshState(t.Context(), AURPackageResourceModel{
		Name: types.StringValue("playerctl"),
	})
	if err != nil {
		t.Fatalf("refresh dependency state: %s", err)
	}
	if !installed {
		t.Fatal("expected playerctl to be installed")
	}
	if state.InstallReason.ValueString() != packageInstallReasonDependency {
		t.Fatalf("observed install reason %q, want dependency", state.InstallReason.ValueString())
	}

	err = r.syncPackage(t.Context(), AURPackageResourceModel{
		Name:          types.StringValue("playerctl"),
		InstallReason: types.StringValue(packageInstallReasonExplicit),
	})
	if err != nil {
		t.Fatalf("sync install reason: %s", err)
	}
	if want := []string{"playerctl"}; !reflect.DeepEqual(manager.marked, want) {
		t.Fatalf("marked %#v, want %#v", manager.marked, want)
	}
}

func TestAURPackageResourceSyncUpgradesWhenVersionIsNotIgnored(t *testing.T) {
	t.Parallel()

	manager := &recordingAURPackageManager{
		fakeAURPackageManager: fakeAURPackageManager{
			statuses: map[string]PackageStatus{
				"claude-code": {
					Name:             "claude-code",
					Installed:        true,
					ReasonUser:       true,
					InstalledVersion: "2.1.201-1",
					CandidateVersion: "2.1.205-1",
					UpgradeVersion:   "2.1.205-1",
				},
			},
		},
	}
	resource := &AURPackageResource{manager: manager}

	err := resource.syncPackage(t.Context(), AURPackageResourceModel{
		Name:          types.StringValue("claude-code"),
		Version:       types.StringValue(versionLatest),
		IgnoreVersion: types.BoolValue(false),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if want := []string{"claude-code"}; !reflect.DeepEqual(manager.upgraded, want) {
		t.Fatalf("upgraded %#v, want %#v", manager.upgraded, want)
	}
	if len(manager.installed) != 0 {
		t.Fatalf("expected no installs, got %#v", manager.installed)
	}
	if want := []bool{true}; !reflect.DeepEqual(manager.remoteLookups, want) {
		t.Fatalf("remote lookups %#v, want %#v", manager.remoteLookups, want)
	}
}

func TestAURPackageResourceSyncInstallsAndMarksMissingPackage(t *testing.T) {
	t.Parallel()

	manager := &recordingAURPackageManager{
		fakeAURPackageManager: fakeAURPackageManager{
			statuses: map[string]PackageStatus{
				"wl-kbptr": {Name: "wl-kbptr"},
			},
		},
	}
	resource := &AURPackageResource{manager: manager}

	err := resource.syncPackage(t.Context(), AURPackageResourceModel{
		Name: types.StringValue("wl-kbptr"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if want := []string{"wl-kbptr"}; !reflect.DeepEqual(manager.installed, want) {
		t.Fatalf("installed %#v, want %#v", manager.installed, want)
	}
	if want := []string{"wl-kbptr"}; !reflect.DeepEqual(manager.marked, want) {
		t.Fatalf("marked %#v, want %#v", manager.marked, want)
	}
}
