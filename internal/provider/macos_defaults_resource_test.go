package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMacOSDefaultsSpecsFromModelParsesDefaultsMap(t *testing.T) {
	t.Parallel()

	defaults := mustMacOSDefaultsMap(t, map[string]MacOSDefaultsDefaultModel{
		"dock_autohide": {
			Domain: types.StringValue("com.apple.dock"),
			Key:    types.StringValue("autohide"),
			Bool:   types.BoolValue(true),
		},
		"languages": {
			Domain:     types.StringValue("NSGlobalDomain"),
			Key:        types.StringValue("AppleLanguages"),
			StringList: mustStringList(t, []string{"ko-KR", "en-US"}),
		},
	})

	specs, diags := macOSDefaultsSpecsFromModel(context.Background(), MacOSDefaultsResourceModel{
		Defaults: defaults,
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsError(diags))
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2", len(specs))
	}
	if specs[0].Name != "dock_autohide" || specs[0].Spec.ID != "user:com.apple.dock:autohide" {
		t.Fatalf("unexpected first spec: %#v", specs[0])
	}
	if specs[1].Name != "languages" || !reflect.DeepEqual(specs[1].Spec.Value.StringList, []string{"ko-KR", "en-US"}) {
		t.Fatalf("unexpected second spec: %#v", specs[1])
	}
}

func TestMacOSDefaultsSpecsFromModelRejectsDuplicateDefaults(t *testing.T) {
	t.Parallel()

	defaults := mustMacOSDefaultsMap(t, map[string]MacOSDefaultsDefaultModel{
		"dock_autohide": {
			Domain: types.StringValue("com.apple.dock"),
			Key:    types.StringValue("autohide"),
			Bool:   types.BoolValue(true),
		},
		"dock_autohide_again": {
			Domain: types.StringValue("com.apple.dock"),
			Key:    types.StringValue("autohide"),
			Bool:   types.BoolValue(false),
		},
	})

	_, diags := macOSDefaultsSpecsFromModel(context.Background(), MacOSDefaultsResourceModel{
		Defaults: defaults,
	})
	if !diags.HasError() {
		t.Fatal("expected duplicate default diagnostics")
	}
}

func TestMacOSDefaultsResourceSyncWritesDefaultsAndRestartsOnce(t *testing.T) {
	t.Parallel()

	defaults := mustMacOSDefaultsMap(t, map[string]MacOSDefaultsDefaultModel{
		"dock_autohide": {
			Domain: types.StringValue("com.apple.dock"),
			Key:    types.StringValue("autohide"),
			Bool:   types.BoolValue(true),
		},
		"dock_hide_recent_apps": {
			Domain: types.StringValue("com.apple.dock"),
			Key:    types.StringValue("show-recents"),
			Bool:   types.BoolValue(false),
		},
	})

	manager := &recordingMacOSDefaultsManager{}
	resource := &MacOSDefaultsResource{
		manager: manager,
	}
	state, err := resource.syncDefaults(context.Background(), MacOSDefaultsResourceModel{
		Defaults: defaults,
	})
	if err != nil {
		t.Fatalf("syncDefaults: %s", err)
	}
	if state.ID.ValueString() != macOSDefaultsResourceID {
		t.Fatalf("id got %q", state.ID.ValueString())
	}
	if len(manager.writes) != 2 {
		t.Fatalf("writes got %d, want 2", len(manager.writes))
	}
	if !reflect.DeepEqual(manager.restarts, []string{"Dock"}) {
		t.Fatalf("restarts got %#v, want Dock once", manager.restarts)
	}
}

func TestMacOSDefaultsResourceUpdateDeletesRemovedDefaultsWhenConfigured(t *testing.T) {
	t.Parallel()

	prior := mustMacOSDefaultsMap(t, map[string]MacOSDefaultsDefaultModel{
		"old_setting": {
			Domain:          types.StringValue("com.apple.dock"),
			Key:             types.StringValue("autohide"),
			Bool:            types.BoolValue(true),
			DeleteOnDestroy: types.BoolValue(true),
		},
	})
	plan := mustMacOSDefaultsMap(t, map[string]MacOSDefaultsDefaultModel{
		"new_setting": {
			Domain: types.StringValue("com.apple.dock"),
			Key:    types.StringValue("show-recents"),
			Bool:   types.BoolValue(false),
		},
	})

	manager := &recordingMacOSDefaultsManager{}
	resource := &MacOSDefaultsResource{
		manager: manager,
	}
	if _, err := resource.updateDefaults(
		context.Background(),
		MacOSDefaultsResourceModel{Defaults: prior},
		MacOSDefaultsResourceModel{Defaults: plan},
	); err != nil {
		t.Fatalf("updateDefaults: %s", err)
	}
	if len(manager.deletes) != 1 {
		t.Fatalf("deletes got %d, want 1", len(manager.deletes))
	}
	if manager.deletes[0].ID != "user:com.apple.dock:autohide" {
		t.Fatalf("deleted id got %q", manager.deletes[0].ID)
	}
	if len(manager.writes) != 1 || manager.writes[0].ID != "user:com.apple.dock:show-recents" {
		t.Fatalf("writes got %#v, want show-recents", manager.writes)
	}
}

func mustMacOSDefaultsMap(t *testing.T, values map[string]MacOSDefaultsDefaultModel) types.Map {
	t.Helper()

	elements := make(map[string]attr.Value, len(values))
	for name, value := range values {
		if value.StringList.IsNull() {
			value.StringList = types.ListNull(types.StringType)
		}
		if value.Restart.IsNull() {
			value.Restart = types.ListNull(types.StringType)
		}

		objectValue, diags := types.ObjectValue(macOSDefaultsDefaultAttributeTypes(), map[string]attr.Value{
			"domain":            value.Domain,
			"key":               value.Key,
			"current_host":      value.CurrentHost,
			"bool":              value.Bool,
			"int":               value.Int,
			"float":             value.Float,
			"string":            value.String,
			"string_list":       value.StringList,
			"delete_on_destroy": value.DeleteOnDestroy,
			"restart":           value.Restart,
		})
		if diags.HasError() {
			t.Fatalf("object value: %s", diagnosticsError(diags))
		}
		elements[name] = objectValue
	}

	mapValue, diags := types.MapValue(macOSDefaultsDefaultObjectType(), elements)
	if diags.HasError() {
		t.Fatalf("map value: %s", diagnosticsError(diags))
	}
	return mapValue
}

func mustStringList(t *testing.T, values []string) types.List {
	t.Helper()

	list, diags := types.ListValueFrom(context.Background(), types.StringType, values)
	if diags.HasError() {
		t.Fatalf("list value: %s", diagnosticsError(diags))
	}
	return list
}

type recordingMacOSDefaultsManager struct {
	writes   []macOSDefaultSpec
	deletes  []macOSDefaultSpec
	restarts []string
}

func (m *recordingMacOSDefaultsManager) ReadDefault(ctx context.Context, spec macOSDefaultSpec) (macOSDefaultValue, bool, error) {
	return macOSDefaultValue{}, false, nil
}

func (m *recordingMacOSDefaultsManager) WriteDefault(ctx context.Context, spec macOSDefaultSpec) error {
	m.writes = append(m.writes, spec)
	return nil
}

func (m *recordingMacOSDefaultsManager) DeleteDefault(ctx context.Context, spec macOSDefaultSpec) error {
	m.deletes = append(m.deletes, spec)
	return nil
}

func (m *recordingMacOSDefaultsManager) RestartProcesses(ctx context.Context, processNames []string) error {
	m.restarts = append(m.restarts, processNames...)
	return nil
}
