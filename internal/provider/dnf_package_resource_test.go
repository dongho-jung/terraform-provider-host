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

	state, installed, err := resource.refreshState(context.Background(), DNFPackageResourceModel{
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

	if state.InstalledVersion.ValueString() != "2.50.1-1.fc44" {
		t.Fatalf("expected installed version, got %q", state.InstalledVersion.ValueString())
	}

	if state.CandidateVersion.ValueString() != "2.50.1-1.fc44" {
		t.Fatalf("expected candidate version, got %q", state.CandidateVersion.ValueString())
	}
}

func TestDNFPackageResourceRefreshStateMissingPackage(t *testing.T) {
	t.Parallel()

	resource := &DNFPackageResource{
		manager: fakePackageManager{
			statuses: map[string]PackageStatus{},
		},
	}

	_, installed, err := resource.refreshState(context.Background(), DNFPackageResourceModel{
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

func TestValidatePackageName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"git", "nodejs22", "NetworkManager-openvpn-gnome"} {
		if err := validatePackageName(name); err != nil {
			t.Fatalf("expected %q to be valid: %s", name, err)
		}
	}

	for _, name := range []string{"", " git", "git "} {
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
