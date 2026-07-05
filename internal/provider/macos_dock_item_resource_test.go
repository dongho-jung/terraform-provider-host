package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type fakeMacOSDockManager struct {
	dock     MacOSDockSpec
	writes   int
	restarts int
}

func (m *fakeMacOSDockManager) ReadDock(ctx context.Context) (MacOSDockSpec, error) {
	return m.dock, nil
}

func (m *fakeMacOSDockManager) WriteDock(ctx context.Context, spec MacOSDockSpec) error {
	m.writes++
	m.dock = spec
	return nil
}

func (m *fakeMacOSDockManager) RestartDock(ctx context.Context) error {
	m.restarts++
	return nil
}

func TestMacOSDockItemImportModelWritesManagedStateWithoutWritingDock(t *testing.T) {
	t.Parallel()

	appDir := t.TempDir()
	chromePath := filepath.Join(appDir, "Google Chrome.app")
	if err := os.Mkdir(chromePath, 0o755); err != nil {
		t.Fatalf("create app fixture: %s", err)
	}

	manager := &fakeMacOSDockManager{
		dock: MacOSDockSpec{
			Apps: []string{chromePath},
		},
	}
	resource := MacOSDockItemResource{
		kind:       macOSDockItemKindApp,
		manager:    manager,
		homeDir:    "/Users/dongho",
		runtimeDir: t.TempDir(),
	}

	model, err := resource.importModel(context.Background(), "20,"+chromePath)
	if err != nil {
		t.Fatalf("import model: %s", err)
	}
	if model.Path.ValueString() != chromePath {
		t.Fatalf("path got %q, want %q", model.Path.ValueString(), chromePath)
	}
	if model.PathResolved.ValueString() != chromePath {
		t.Fatalf("path_resolved got %q, want %q", model.PathResolved.ValueString(), chromePath)
	}
	if model.Priority.ValueInt64() != 20 {
		t.Fatalf("priority got %d, want 20", model.Priority.ValueInt64())
	}
	if err := validateMacOSDockManagedItemID(model.ID.ValueString()); err != nil {
		t.Fatalf("invalid imported ID: %s", err)
	}

	item, exists, err := readMacOSDockManagedItemForRuntime(model.ID.ValueString(), macOSDockItemKindApp, resource.runtimeDir)
	if err != nil {
		t.Fatalf("read managed item: %s", err)
	}
	if !exists {
		t.Fatal("managed item was not written")
	}
	if item.Path != chromePath || item.Priority != 20 {
		t.Fatalf("managed item got %#v", item)
	}
	if manager.writes != 0 || manager.restarts != 0 {
		t.Fatalf("import should not write or restart Dock, got writes=%d restarts=%d", manager.writes, manager.restarts)
	}
}

func TestMacOSDockItemImportModelInfersPriorityFromLiveOrder(t *testing.T) {
	t.Parallel()

	appDir := t.TempDir()
	settingsPath := filepath.Join(appDir, "System Settings.app")
	chromePath := filepath.Join(appDir, "Google Chrome.app")
	for _, path := range []string{settingsPath, chromePath} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatalf("create app fixture: %s", err)
		}
	}

	resource := MacOSDockItemResource{
		kind: macOSDockItemKindApp,
		manager: &fakeMacOSDockManager{
			dock: MacOSDockSpec{
				Apps: []string{settingsPath, chromePath},
			},
		},
		homeDir:    "/Users/dongho",
		runtimeDir: t.TempDir(),
	}

	model, err := resource.importModel(context.Background(), chromePath)
	if err != nil {
		t.Fatalf("import model: %s", err)
	}
	if model.Priority.ValueInt64() != 20 {
		t.Fatalf("priority got %d, want 20", model.Priority.ValueInt64())
	}
}

func TestMacOSDockItemImportModelRejectsMissingLiveItem(t *testing.T) {
	t.Parallel()

	appDir := t.TempDir()
	chromePath := filepath.Join(appDir, "Google Chrome.app")
	if err := os.Mkdir(chromePath, 0o755); err != nil {
		t.Fatalf("create app fixture: %s", err)
	}

	resource := MacOSDockItemResource{
		kind:       macOSDockItemKindApp,
		manager:    &fakeMacOSDockManager{},
		homeDir:    "/Users/dongho",
		runtimeDir: t.TempDir(),
	}

	if _, err := resource.importModel(context.Background(), chromePath); err == nil {
		t.Fatal("expected missing live Dock item to fail")
	}
}
