package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

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

	state, installed, err := resource.refreshState(context.Background(), PacmanPackageResourceModel{
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
	if state.InstalledVersion.ValueString() != "2.50.1-1" {
		t.Fatalf("expected installed version, got %q", state.InstalledVersion.ValueString())
	}
	if !state.CandidateVersion.IsNull() {
		t.Fatalf("expected ignored candidate version to be null, got %#v", state.CandidateVersion)
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

	err := resource.syncPackage(context.Background(), PacmanPackageResourceModel{
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
