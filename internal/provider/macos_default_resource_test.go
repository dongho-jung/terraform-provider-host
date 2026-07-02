package provider

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMacOSDefaultValueFromModelRequiresExactlyOneValue(t *testing.T) {
	t.Parallel()

	_, diags := macOSDefaultValueFromModel(context.Background(), MacOSDefaultResourceModel{})
	if !diags.HasError() {
		t.Fatal("expected error when no value is configured")
	}

	_, diags = macOSDefaultValueFromModel(context.Background(), MacOSDefaultResourceModel{
		Bool:   types.BoolValue(true),
		String: types.StringValue("true"),
	})
	if !diags.HasError() {
		t.Fatal("expected error when multiple values are configured")
	}

	value, diags := macOSDefaultValueFromModel(context.Background(), MacOSDefaultResourceModel{
		Bool: types.BoolValue(true),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsError(diags))
	}
	if value.Type != macOSDefaultValueBool || !value.Bool {
		t.Fatalf("got %#v, want true bool", value)
	}
}

func TestMacOSDefaultWriteArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value macOSDefaultValue
		want  []string
	}{
		{
			name:  "bool",
			value: macOSDefaultValue{Type: macOSDefaultValueBool, Bool: true},
			want:  []string{"-bool", "true"},
		},
		{
			name:  "int",
			value: macOSDefaultValue{Type: macOSDefaultValueInt, Int: 42},
			want:  []string{"-int", "42"},
		},
		{
			name:  "float",
			value: macOSDefaultValue{Type: macOSDefaultValueFloat, Float: 1.5},
			want:  []string{"-float", "1.5"},
		},
		{
			name:  "string",
			value: macOSDefaultValue{Type: macOSDefaultValueString, String: "Dock"},
			want:  []string{"-string", "Dock"},
		},
		{
			name:  "string_list",
			value: macOSDefaultValue{Type: macOSDefaultValueStringList, StringList: []string{"ko-KR", "en-US"}},
			want:  []string{"-array", "ko-KR", "en-US"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := macOSDefaultWriteArgs(tt.value); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestMacOSDefaultSpecUsesDefaultRestartsWhenRestartOmitted(t *testing.T) {
	t.Parallel()

	spec, diags := macOSDefaultSpecFromModel(context.Background(), MacOSDefaultResourceModel{
		Domain:          types.StringValue("com.apple.dock"),
		Key:             types.StringValue("autohide"),
		CurrentHost:     types.BoolValue(false),
		Bool:            types.BoolValue(true),
		DeleteOnDestroy: types.BoolValue(false),
		Restart:         types.ListNull(types.StringType),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsError(diags))
	}
	if !reflect.DeepEqual(spec.Restart, []string{"Dock"}) {
		t.Fatalf("restart got %#v, want Dock", spec.Restart)
	}
}

func TestMacOSDefaultSpecRespectsEmptyRestartList(t *testing.T) {
	t.Parallel()

	emptyRestart, diags := types.ListValueFrom(context.Background(), types.StringType, []string{})
	if diags.HasError() {
		t.Fatalf("build empty restart list: %s", diagnosticsError(diags))
	}

	spec, specDiags := macOSDefaultSpecFromModel(context.Background(), MacOSDefaultResourceModel{
		Domain:          types.StringValue("com.apple.dock"),
		Key:             types.StringValue("autohide"),
		CurrentHost:     types.BoolValue(false),
		Bool:            types.BoolValue(true),
		DeleteOnDestroy: types.BoolValue(false),
		Restart:         emptyRestart,
	})
	if specDiags.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsError(specDiags))
	}
	if len(spec.Restart) != 0 {
		t.Fatalf("restart got %#v, want empty", spec.Restart)
	}
}

func TestMacOSDefaultImportSpec(t *testing.T) {
	t.Parallel()

	spec, err := macOSDefaultImportSpec("currentHost:com.apple.dock:autohide")
	if err != nil {
		t.Fatalf("macOSDefaultImportSpec: %s", err)
	}
	if spec.ID != "currentHost:com.apple.dock:autohide" {
		t.Fatalf("id got %q", spec.ID)
	}
	if !spec.CurrentHost {
		t.Fatal("expected current host scope")
	}
	if spec.Domain != "com.apple.dock" || spec.Key != "autohide" {
		t.Fatalf("got domain=%q key=%q", spec.Domain, spec.Key)
	}

	spec, err = macOSDefaultImportSpec("NSGlobalDomain:AppleLanguages")
	if err != nil {
		t.Fatalf("macOSDefaultImportSpec without scope: %s", err)
	}
	if spec.ID != "user:NSGlobalDomain:AppleLanguages" {
		t.Fatalf("id got %q", spec.ID)
	}
	if spec.CurrentHost {
		t.Fatal("expected user scope")
	}
}

func TestMacOSDefaultResourceImportStateReadsCurrentValue(t *testing.T) {
	t.Parallel()

	resource := &MacOSDefaultResource{
		manager: fakeMacOSDefaultsManager{
			values: map[string]macOSDefaultValue{
				"user:com.apple.dock:autohide": {
					Type: macOSDefaultValueBool,
					Bool: true,
				},
			},
		},
	}
	state, err := resource.importDefaultState(context.Background(), "user:com.apple.dock:autohide")
	if err != nil {
		t.Fatalf("importDefaultState: %s", err)
	}
	if state.ID.ValueString() != "user:com.apple.dock:autohide" {
		t.Fatalf("id got %q", state.ID.ValueString())
	}
	if state.Domain.ValueString() != "com.apple.dock" || state.Key.ValueString() != "autohide" {
		t.Fatalf("got domain=%q key=%q", state.Domain.ValueString(), state.Key.ValueString())
	}
	if state.Bool.IsNull() || !state.Bool.ValueBool() {
		t.Fatalf("expected imported bool true, got %#v", state.Bool)
	}
}

func TestParseMacOSDefaultReadValue(t *testing.T) {
	t.Parallel()

	value, err := parseMacOSDefaultReadValue(macOSDefaultValueBool, "1\n")
	if err != nil {
		t.Fatalf("parse bool: %s", err)
	}
	if !value.Bool {
		t.Fatal("expected true")
	}

	value, err = parseMacOSDefaultReadValue(macOSDefaultValueStringList, "(\n    \"en-US\",\n    \"ko-KR\"\n)\n")
	if err != nil {
		t.Fatalf("parse string list: %s", err)
	}
	if !reflect.DeepEqual(value.StringList, []string{"en-US", "ko-KR"}) {
		t.Fatalf("got %#v", value.StringList)
	}
}

type fakeMacOSDefaultsManager struct {
	values map[string]macOSDefaultValue
}

func (m fakeMacOSDefaultsManager) ReadDefault(ctx context.Context, spec macOSDefaultSpec) (macOSDefaultValue, bool, error) {
	value, ok := m.values[spec.ID]
	return value, ok, nil
}

func (m fakeMacOSDefaultsManager) WriteDefault(ctx context.Context, spec macOSDefaultSpec) error {
	return nil
}

func (m fakeMacOSDefaultsManager) DeleteDefault(ctx context.Context, spec macOSDefaultSpec) error {
	return nil
}

func (m fakeMacOSDefaultsManager) RestartProcesses(ctx context.Context, processNames []string) error {
	return nil
}

func TestCLIMacOSDefaultsManagerCommands(t *testing.T) {
	t.Parallel()

	var calls []string
	manager := &CLIMacOSDefaultsManager{
		defaultsPath: "defaults",
		killallPath:  "killall",
		run: func(ctx context.Context, command string, args ...string) ([]byte, error) {
			calls = append(calls, command+" "+strings.Join(args, " "))
			return nil, nil
		},
	}

	spec := macOSDefaultSpec{
		Domain:      "com.apple.dock",
		Key:         "autohide",
		CurrentHost: true,
		Restart:     []string{"Dock"},
		Value:       macOSDefaultValue{Type: macOSDefaultValueBool, Bool: true},
	}

	if err := manager.WriteDefault(context.Background(), spec); err != nil {
		t.Fatalf("WriteDefault: %s", err)
	}
	if err := manager.RestartProcesses(context.Background(), spec.Restart); err != nil {
		t.Fatalf("RestartProcesses: %s", err)
	}

	want := []string{
		"defaults -currentHost write com.apple.dock autohide -bool true",
		"killall Dock",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("got %#v, want %#v", calls, want)
	}
}

func TestCLIMacOSDefaultsManagerRead(t *testing.T) {
	t.Parallel()

	manager := &CLIMacOSDefaultsManager{
		defaultsPath: "defaults",
		run: func(ctx context.Context, command string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "read-type"):
				return []byte("Type is array\n"), nil
			case strings.Contains(joined, "read"):
				return []byte("(\n    \"en-US\",\n    \"ko-KR\"\n)\n"), nil
			default:
				t.Fatalf("unexpected command %s %s", command, joined)
				return nil, nil
			}
		},
	}

	value, exists, err := manager.ReadDefault(context.Background(), macOSDefaultSpec{
		Domain: "NSGlobalDomain",
		Key:    "AppleLanguages",
	})
	if err != nil {
		t.Fatalf("ReadDefault: %s", err)
	}
	if !exists {
		t.Fatal("expected value to exist")
	}
	if !reflect.DeepEqual(value.StringList, []string{"en-US", "ko-KR"}) {
		t.Fatalf("got %#v", value.StringList)
	}
}
