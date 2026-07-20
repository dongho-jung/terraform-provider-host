package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
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
