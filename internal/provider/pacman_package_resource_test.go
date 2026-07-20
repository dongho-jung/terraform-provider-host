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

type convergingPackageManager struct {
	status PackageStatus
	marked []string
}

func (m *convergingPackageManager) PackageStatus(ctx context.Context, name string) (PackageStatus, error) {
	return m.status, nil
}

func (m *convergingPackageManager) InstallPackages(ctx context.Context, names []string) error {
	return nil
}

func (m *convergingPackageManager) UpgradePackages(ctx context.Context, names []string) error {
	return nil
}

func (m *convergingPackageManager) MarkUserPackages(ctx context.Context, names []string) error {
	m.marked = append(m.marked, names...)
	m.status.ReasonUser = true
	return nil
}

func (m *convergingPackageManager) RemovePackages(ctx context.Context, names []string, autoremove bool) error {
	return nil
}

func TestPacmanPackageResourceRefreshStateInstalledExplicitPackage(t *testing.T) {
	t.Parallel()

	resource := &PacmanPackageResource{
		manager: fakePackageManager{
			statuses: map[string]PackageStatus{
				"git": {
					Name:             "git",
					Installed:        true,
					ReasonUser:       true,
					InstalledVersion: "2.50.1-1",
					CandidateVersion: "2.50.1-1",
				},
			},
		},
	}

	state, installed, err := resource.refreshState(t.Context(), PacmanPackageResourceModel{
		Name: types.StringValue("git"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !installed {
		t.Fatal("expected installed package")
	}
	if state.ID.ValueString() != "git" {
		t.Fatalf("expected id git, got %q", state.ID.ValueString())
	}
	if state.Version.ValueString() != versionLatest {
		t.Fatalf("expected version policy latest, got %q", state.Version.ValueString())
	}
	if !state.IgnoreVersion.ValueBool() {
		t.Fatal("expected ignore_version to default to true")
	}
	if state.Autoremove.ValueBool() {
		t.Fatal("expected autoremove to default to false")
	}
	if state.InstallReason.ValueString() != packageInstallReasonExplicit {
		t.Fatalf("expected explicit install reason, got %q", state.InstallReason.ValueString())
	}
	if state.InstalledVersion.ValueString() != "2.50.1-1" {
		t.Fatalf("expected installed version, got %q", state.InstalledVersion.ValueString())
	}
	if !state.CandidateVersion.IsNull() {
		t.Fatalf("expected ignored candidate version to be null, got %#v", state.CandidateVersion)
	}
}

func TestPacmanPackageResourceInstalledDependencyPlansAndAppliesExplicitReason(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	manager := &convergingPackageManager{
		status: PackageStatus{
			Name:             "glib2",
			Installed:        true,
			ReasonUser:       false,
			InstalledVersion: "2.84.4-1",
		},
	}
	r := &PacmanPackageResource{manager: manager}

	var schemaResp frameworkresource.SchemaResponse
	r.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("unexpected schema diagnostics: %v", schemaResp.Diagnostics)
	}

	stateModel, installed, err := r.refreshState(ctx, PacmanPackageResourceModel{
		Name: types.StringValue("glib2"),
	})
	if err != nil {
		t.Fatalf("refresh dependency state: %s", err)
	}
	if !installed {
		t.Fatal("expected glib2 to be installed")
	}
	if stateModel.InstallReason.ValueString() != packageInstallReasonDependency {
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
	planModel.InstallReason = types.StringValue(packageInstallReasonExplicit)
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
	var modifiedPlan PacmanPackageResourceModel
	if diags := modifyResp.Plan.Get(ctx, &modifiedPlan); diags.HasError() {
		t.Fatalf("decode modified plan: %v", diags)
	}
	if modifiedPlan.InstallReason.ValueString() != packageInstallReasonExplicit {
		t.Fatalf("planned install reason %q, want explicit", modifiedPlan.InstallReason.ValueString())
	}
	if stateModel.InstallReason.Equal(modifiedPlan.InstallReason) {
		t.Fatal("expected observed dependency reason to differ from desired explicit reason")
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

	var appliedState PacmanPackageResourceModel
	if diags := updateResp.State.Get(ctx, &appliedState); diags.HasError() {
		t.Fatalf("decode applied state: %v", diags)
	}
	if appliedState.InstallReason.ValueString() != packageInstallReasonExplicit {
		t.Fatalf("applied install reason %q, want explicit", appliedState.InstallReason.ValueString())
	}
}

func TestPacmanPackageResourceSyncUpgradesWhenVersionIsNotIgnored(t *testing.T) {
	t.Parallel()

	manager := &recordingPackageManager{
		statuses: map[string]PackageStatus{
			"git": {
				Name:             "git",
				Installed:        true,
				ReasonUser:       true,
				InstalledVersion: "2.50.1-1",
				CandidateVersion: "2.51.0-1",
				UpgradeVersion:   "2.51.0-1",
			},
		},
	}
	resource := &PacmanPackageResource{manager: manager}

	err := resource.syncPackage(t.Context(), PacmanPackageResourceModel{
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
