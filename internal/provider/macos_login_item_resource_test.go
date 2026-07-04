package provider

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestParseMacOSLoginItems(t *testing.T) {
	t.Parallel()

	output := "Itsycal\t/Applications/Itsycal.app\tfalse\nShottr\t/Applications/Shottr.app\ttrue\n"
	items, err := parseMacOSLoginItems(output)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Name != "Itsycal" || items[0].PathResolved != "/Applications/Itsycal.app" || items[0].Hidden {
		t.Fatalf("unexpected first item: %#v", items[0])
	}
	if items[1].Name != "Shottr" || !items[1].Hidden {
		t.Fatalf("unexpected second item: %#v", items[1])
	}
}

func TestMacOSLoginItemSpecFromModel(t *testing.T) {
	t.Parallel()

	model := MacOSLoginItemResourceModel{
		Path:   types.StringValue("/Applications/Hammerspoon.app"),
		Hidden: types.BoolValue(true),
	}
	spec, err := macOSLoginItemSpecFromModel(model)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if spec.PathResolved != "/Applications/Hammerspoon.app" || !spec.Hidden {
		t.Fatalf("unexpected spec: %#v", spec)
	}
}

func TestMacOSLoginItemSpecRejectsNonAppPath(t *testing.T) {
	t.Parallel()

	model := MacOSLoginItemResourceModel{
		Path:   types.StringValue("/Applications/Hammerspoon"),
		Hidden: types.BoolValue(false),
	}
	if _, err := macOSLoginItemSpecFromModel(model); err == nil {
		t.Fatal("expected non-app path error")
	}
}

func TestMacOSLoginItemResourceSyncLoginItem(t *testing.T) {
	t.Parallel()

	appPath := filepath.Join(t.TempDir(), "Example.app")
	manager := &fakeMacOSLoginItemManager{
		items: map[string]MacOSLoginItemStatus{},
	}
	resource := &MacOSLoginItemResource{manager: manager}
	model := MacOSLoginItemResourceModel{
		Path:   types.StringValue(appPath),
		Hidden: types.BoolValue(true),
	}

	state, err := resource.syncLoginItem(context.Background(), model)
	if err != nil {
		t.Fatalf("sync login item: %s", err)
	}
	if state.Name.ValueString() != "Example" {
		t.Fatalf("name got %q, want Example", state.Name.ValueString())
	}
	if !state.Hidden.ValueBool() {
		t.Fatal("hidden got false, want true")
	}

	status, exists, err := manager.LoginItemStatus(context.Background(), appPath)
	if err != nil {
		t.Fatalf("read fake login item: %s", err)
	}
	if !exists {
		t.Fatal("fake login item was not created")
	}
	if status.PathResolved != appPath || !status.Hidden {
		t.Fatalf("unexpected fake status: %#v", status)
	}
}

type fakeMacOSLoginItemManager struct {
	items map[string]MacOSLoginItemStatus
}

func (m *fakeMacOSLoginItemManager) LoginItemStatus(ctx context.Context, path string) (MacOSLoginItemStatus, bool, error) {
	status, ok := m.items[path]
	return status, ok, nil
}

func (m *fakeMacOSLoginItemManager) EnsureLoginItem(ctx context.Context, spec MacOSLoginItemSpec) (MacOSLoginItemStatus, error) {
	status := MacOSLoginItemStatus{
		Path:         spec.PathResolved,
		PathResolved: spec.PathResolved,
		Name:         strings.TrimSuffix(filepath.Base(spec.PathResolved), filepath.Ext(spec.PathResolved)),
		Hidden:       spec.Hidden,
	}
	m.items[spec.PathResolved] = status
	return status, nil
}

func (m *fakeMacOSLoginItemManager) DeleteLoginItem(ctx context.Context, path string) error {
	delete(m.items, path)
	return nil
}
