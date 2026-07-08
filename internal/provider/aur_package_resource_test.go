package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

type fakeAURPackageManager struct {
	statuses      map[string]PackageStatus
	remoteLookups []bool
}

func (m *fakeAURPackageManager) PackageStatus(ctx context.Context, name string, includeRemote bool) (PackageStatus, error) {
	m.remoteLookups = append(m.remoteLookups, includeRemote)
	return m.statuses[name], nil
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
	if !state.CandidateVersion.IsNull() {
		t.Fatalf("expected ignored candidate version to be null, got %#v", state.CandidateVersion)
	}
	if want := []bool{false}; !reflect.DeepEqual(manager.remoteLookups, want) {
		t.Fatalf("remote lookups %#v, want %#v", manager.remoteLookups, want)
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
