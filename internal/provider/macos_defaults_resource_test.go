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

	settings := mustMacOSDefaultsMap(t, map[string]MacOSDefaultsDefaultModel{
		"dock_autohide": {
			Domain: mustMacOSSettingDomain(t, "com.apple.dock"),
			Key:    types.StringValue("autohide"),
			Value:  mustMacOSDefaultDynamic(t, macOSDefaultValue{Type: macOSDefaultValueBool, Bool: true}),
		},
		"languages": {
			Domain: mustMacOSSettingDomain(t, "NSGlobalDomain"),
			Key:    types.StringValue("AppleLanguages"),
			Value:  mustMacOSDefaultDynamic(t, macOSDefaultValue{Type: macOSDefaultValueStringList, StringList: []string{"ko-KR", "en-US"}}),
		},
	})

	specs, diags := macOSDefaultsSpecsFromModel(context.Background(), MacOSDefaultsResourceModel{
		Settings: settings,
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

	settings := mustMacOSDefaultsMap(t, map[string]MacOSDefaultsDefaultModel{
		"dock_autohide": {
			Domain: mustMacOSSettingDomain(t, "com.apple.dock"),
			Key:    types.StringValue("autohide"),
			Value:  mustMacOSDefaultDynamic(t, macOSDefaultValue{Type: macOSDefaultValueBool, Bool: true}),
		},
		"dock_autohide_again": {
			Domain: mustMacOSSettingDomain(t, "com.apple.dock"),
			Key:    types.StringValue("autohide"),
			Value:  mustMacOSDefaultDynamic(t, macOSDefaultValue{Type: macOSDefaultValueBool, Bool: false}),
		},
	})

	_, diags := macOSDefaultsSpecsFromModel(context.Background(), MacOSDefaultsResourceModel{
		Settings: settings,
	})
	if !diags.HasError() {
		t.Fatal("expected duplicate default diagnostics")
	}
}

func TestMacOSDefaultsSpecsFromModelParsesGroups(t *testing.T) {
	t.Parallel()

	groups := mustMacOSSettingsGroupsMap(t, map[string]MacOSSettingsGroupModel{
		"clock": {
			Domain: mustMacOSSettingDomain(t, "com.apple.menuextra.clock"),
			Settings: mustMacOSSettingsGroupSettingsMap(t, map[string]MacOSSettingsGroupSettingModel{
				"analog": {
					Key:   types.StringValue("IsAnalog"),
					Value: mustMacOSDefaultDynamic(t, macOSDefaultValue{Type: macOSDefaultValueBool, Bool: true}),
				},
				"show_ampm": {
					Key:   types.StringValue("ShowAMPM"),
					Value: mustMacOSDefaultDynamic(t, macOSDefaultValue{Type: macOSDefaultValueBool, Bool: true}),
				},
			}),
		},
	})

	specs, diags := macOSDefaultsSpecsFromModel(context.Background(), MacOSDefaultsResourceModel{
		Groups: groups,
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsError(diags))
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2", len(specs))
	}
	if specs[0].GroupName != "clock" || specs[0].Name != "analog" || specs[0].Spec.ID != "user:com.apple.menuextra.clock:IsAnalog" {
		t.Fatalf("unexpected first grouped spec: %#v", specs[0])
	}
	if specs[1].GroupName != "clock" || specs[1].Name != "show_ampm" || specs[1].Spec.ID != "user:com.apple.menuextra.clock:ShowAMPM" {
		t.Fatalf("unexpected second grouped spec: %#v", specs[1])
	}
}

func TestMacOSDefaultsImportSpecs(t *testing.T) {
	t.Parallel()

	specs, err := macOSDefaultsImportSpecs("dock_autohide=user:com.apple.dock:autohide,languages=NSGlobalDomain:AppleLanguages")
	if err != nil {
		t.Fatalf("macOSDefaultsImportSpecs: %s", err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2", len(specs))
	}
	if specs[0].Name != "dock_autohide" || specs[0].Spec.ID != "user:com.apple.dock:autohide" {
		t.Fatalf("unexpected first spec: %#v", specs[0])
	}
	if specs[1].Name != "languages" || specs[1].Spec.ID != "user:NSGlobalDomain:AppleLanguages" {
		t.Fatalf("unexpected second spec: %#v", specs[1])
	}
}

func TestMacOSDefaultsImportSpecsParsesGroupedNames(t *testing.T) {
	t.Parallel()

	specs, err := macOSDefaultsImportSpecs("clock.analog=user:com.apple.menuextra.clock:IsAnalog,clock.show_ampm=user:com.apple.menuextra.clock:ShowAMPM")
	if err != nil {
		t.Fatalf("macOSDefaultsImportSpecs: %s", err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2", len(specs))
	}
	if specs[0].GroupName != "clock" || specs[0].Name != "analog" || specs[0].Spec.ID != "user:com.apple.menuextra.clock:IsAnalog" {
		t.Fatalf("unexpected first grouped import: %#v", specs[0])
	}
	if specs[1].GroupName != "clock" || specs[1].Name != "show_ampm" || specs[1].Spec.ID != "user:com.apple.menuextra.clock:ShowAMPM" {
		t.Fatalf("unexpected second grouped import: %#v", specs[1])
	}
}

func TestMacOSDefaultsImportSpecsRejectsDuplicateDefaults(t *testing.T) {
	t.Parallel()

	_, err := macOSDefaultsImportSpecs("first=user:com.apple.dock:autohide,second=com.apple.dock:autohide")
	if err == nil {
		t.Fatal("expected duplicate import error")
	}
}

func TestMacOSDefaultsResourceImportStateReadsCurrentValues(t *testing.T) {
	t.Parallel()

	resource := &MacOSDefaultsResource{
		manager: fakeMacOSDefaultsManager{
			values: map[string]macOSDefaultValue{
				"user:com.apple.dock:autohide": {
					Type: macOSDefaultValueBool,
					Bool: true,
				},
				"user:NSGlobalDomain:AppleLanguages": {
					Type:       macOSDefaultValueStringList,
					StringList: []string{"ko-KR", "en-US"},
				},
			},
		},
	}
	state, err := resource.importDefaultsState(context.Background(), "dock_autohide=user:com.apple.dock:autohide,languages=NSGlobalDomain:AppleLanguages")
	if err != nil {
		t.Fatalf("importDefaultsState: %s", err)
	}
	if state.ID.ValueString() != macOSSettingsResourceID {
		t.Fatalf("id got %q", state.ID.ValueString())
	}

	specs, diags := macOSDefaultsSpecsFromModel(context.Background(), state)
	if diags.HasError() {
		t.Fatalf("settings state: %s", diagnosticsError(diags))
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2", len(specs))
	}
	if specs[0].Name != "dock_autohide" || specs[0].Spec.Value.Type != macOSDefaultValueBool || !specs[0].Spec.Value.Bool {
		t.Fatalf("unexpected dock spec: %#v", specs[0])
	}
	if specs[1].Name != "languages" || !reflect.DeepEqual(specs[1].Spec.Value.StringList, []string{"ko-KR", "en-US"}) {
		t.Fatalf("unexpected languages spec: %#v", specs[1])
	}
}

func TestMacOSDefaultsResourceSyncWritesDefaultsAndRestartsOnce(t *testing.T) {
	t.Parallel()

	settings := mustMacOSDefaultsMap(t, map[string]MacOSDefaultsDefaultModel{
		"dock_autohide": {
			Domain: mustMacOSSettingDomain(t, "com.apple.dock"),
			Key:    types.StringValue("autohide"),
			Value:  mustMacOSDefaultDynamic(t, macOSDefaultValue{Type: macOSDefaultValueBool, Bool: true}),
		},
		"dock_hide_recent_apps": {
			Domain: mustMacOSSettingDomain(t, "com.apple.dock"),
			Key:    types.StringValue("show-recents"),
			Value:  mustMacOSDefaultDynamic(t, macOSDefaultValue{Type: macOSDefaultValueBool, Bool: false}),
		},
	})

	manager := &recordingMacOSDefaultsManager{}
	resource := &MacOSDefaultsResource{
		manager: manager,
	}
	state, err := resource.syncDefaults(context.Background(), MacOSDefaultsResourceModel{
		Settings: settings,
	})
	if err != nil {
		t.Fatalf("syncDefaults: %s", err)
	}
	if state.ID.ValueString() != macOSSettingsResourceID {
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
			Domain:          mustMacOSSettingDomain(t, "com.apple.dock"),
			Key:             types.StringValue("autohide"),
			Value:           mustMacOSDefaultDynamic(t, macOSDefaultValue{Type: macOSDefaultValueBool, Bool: true}),
			DeleteOnDestroy: types.BoolValue(true),
		},
	})
	plan := mustMacOSDefaultsMap(t, map[string]MacOSDefaultsDefaultModel{
		"new_setting": {
			Domain: mustMacOSSettingDomain(t, "com.apple.dock"),
			Key:    types.StringValue("show-recents"),
			Value:  mustMacOSDefaultDynamic(t, macOSDefaultValue{Type: macOSDefaultValueBool, Bool: false}),
		},
	})

	manager := &recordingMacOSDefaultsManager{}
	resource := &MacOSDefaultsResource{
		manager: manager,
	}
	if _, err := resource.updateDefaults(
		context.Background(),
		MacOSDefaultsResourceModel{Settings: prior},
		MacOSDefaultsResourceModel{Settings: plan},
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

func mustMacOSDefaultsMap(t *testing.T, values map[string]MacOSDefaultsDefaultModel) types.Dynamic {
	t.Helper()

	elements := make(map[string]attr.Value, len(values))
	for name, value := range values {
		if value.Value.IsNull() {
			value.Value = types.DynamicNull()
		}
		if value.Restart.IsNull() {
			value.Restart = types.ListNull(types.StringType)
		}
		objectValue, err := macOSDefaultsDefaultObjectValue(context.Background(), value)
		if err != nil {
			t.Fatalf("object value: %s", err)
		}
		elements[name] = objectValue
	}

	dynamic, err := macOSSettingsDynamicObject(context.Background(), elements)
	if err != nil {
		t.Fatalf("settings value: %s", err)
	}
	return dynamic
}

func mustMacOSSettingsGroupsMap(t *testing.T, values map[string]MacOSSettingsGroupModel) types.Dynamic {
	t.Helper()

	elements := make(map[string]attr.Value, len(values))
	for name, value := range values {
		if value.Restart.IsNull() {
			value.Restart = types.ListNull(types.StringType)
		}

		objectValue, err := macOSSettingsGroupObjectValue(context.Background(), value)
		if err != nil {
			t.Fatalf("group object value: %s", err)
		}
		elements[name] = objectValue
	}

	dynamic, err := macOSSettingsDynamicObject(context.Background(), elements)
	if err != nil {
		t.Fatalf("groups value: %s", err)
	}
	return dynamic
}

func mustMacOSSettingsGroupSettingsMap(t *testing.T, values map[string]MacOSSettingsGroupSettingModel) types.Dynamic {
	t.Helper()

	elements := make(map[string]attr.Value, len(values))
	for name, value := range values {
		objectValue, err := macOSSettingsGroupSettingObjectValue(context.Background(), value)
		if err != nil {
			t.Fatalf("group setting object value: %s", err)
		}
		elements[name] = objectValue
	}

	dynamic, err := macOSSettingsDynamicObject(context.Background(), elements)
	if err != nil {
		t.Fatalf("group settings value: %s", err)
	}
	return dynamic
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
