package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestLookupMacOSPermissionService(t *testing.T) {
	t.Parallel()

	service, err := lookupMacOSPermissionService("input_monitoring")
	if err != nil {
		t.Fatalf("lookupMacOSPermissionService: %s", err)
	}
	if service.Service != "kTCCServiceListenEvent" {
		t.Fatalf("got %q, want kTCCServiceListenEvent", service.Service)
	}
	if service.ResetName() != "ListenEvent" {
		t.Fatalf("got %q, want ListenEvent", service.ResetName())
	}
	if !strings.HasSuffix(service.SettingsURL(), "?Privacy_ListenEvent") {
		t.Fatalf("got %q", service.SettingsURL())
	}

	raw, err := lookupMacOSPermissionService("kTCCServiceAccessibility")
	if err != nil {
		t.Fatalf("lookupMacOSPermissionService: %s", err)
	}
	if raw.Name != "accessibility" || !raw.System {
		t.Fatalf("got %#v, want the system accessibility service", raw)
	}

	if _, err := lookupMacOSPermissionService("not_a_service"); err == nil {
		t.Fatal("expected an unknown service to be rejected")
	}
}

func TestMacOSPermissionSpecFromModel(t *testing.T) {
	t.Parallel()

	spec, diags := macOSPermissionSpecFromModel(MacOSPermissionResourceModel{
		Service: types.StringValue("accessibility"),
		Client:  types.StringValue("org.hammerspoon.Hammerspoon"),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsError(diags))
	}
	if spec.ID != "accessibility:org.hammerspoon.Hammerspoon" {
		t.Fatalf("got %q", spec.ID)
	}
	if spec.ClientType != macOSPermissionClientBundleID {
		t.Fatalf("got %q, want %q", spec.ClientType, macOSPermissionClientBundleID)
	}

	pathSpec, diags := macOSPermissionSpecFromModel(MacOSPermissionResourceModel{
		Service: types.StringValue("full_disk_access"),
		Client:  types.StringValue("/opt/homebrew/bin/terraform"),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsError(diags))
	}
	if pathSpec.ClientType != macOSPermissionClientPath {
		t.Fatalf("got %q, want %q", pathSpec.ClientType, macOSPermissionClientPath)
	}

	if _, diags := macOSPermissionSpecFromModel(MacOSPermissionResourceModel{
		Service: types.StringValue("accessibility"),
		Client:  types.StringValue("   "),
	}); !diags.HasError() {
		t.Fatal("expected an empty client to be rejected")
	}
}

func TestMacOSPermissionDatabaseURIEscapesSpaces(t *testing.T) {
	t.Parallel()

	uri := macOSPermissionDatabaseURI("/Library/Application Support/com.apple.TCC/TCC.db")
	want := "file:///Library/Application%20Support/com.apple.TCC/TCC.db?immutable=1"
	if uri != want {
		t.Fatalf("got %q, want %q", uri, want)
	}
}

func TestSQLiteQuoteEscapesQuotes(t *testing.T) {
	t.Parallel()

	if got := sqliteQuote("o'brien"); got != "'o''brien'" {
		t.Fatalf("got %q", got)
	}
}

type stubMacOSPermissionManager struct {
	state    string
	stateErr error
	opened   []string
	revoked  []string
}

func (s *stubMacOSPermissionManager) PermissionState(ctx context.Context, spec macOSPermissionSpec) (string, error) {
	return s.state, s.stateErr
}

func (s *stubMacOSPermissionManager) RevokePermission(ctx context.Context, spec macOSPermissionSpec) error {
	s.revoked = append(s.revoked, spec.Service.ResetName()+" "+spec.Client)
	return nil
}

func (s *stubMacOSPermissionManager) OpenSettings(ctx context.Context, settingsURL string) error {
	s.opened = append(s.opened, settingsURL)
	return nil
}

func macOSPermissionTestModel() MacOSPermissionResourceModel {
	return MacOSPermissionResourceModel{
		Service:                 types.StringValue("accessibility"),
		Client:                  types.StringValue("org.hammerspoon.Hammerspoon"),
		Required:                types.BoolValue(true),
		OpenSettingsWhenMissing: types.BoolValue(false),
		RevokeOnDestroy:         types.BoolValue(false),
	}
}

func TestMacOSPermissionVerify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		state       string
		required    bool
		wantGranted bool
		wantError   bool
		wantWarning bool
	}{
		{name: "granted", state: macOSPermissionStateGranted, required: true, wantGranted: true},
		{name: "missing and required", state: macOSPermissionStateMissing, required: true, wantError: true},
		{name: "denied and required", state: macOSPermissionStateDenied, required: true, wantError: true},
		{name: "missing and optional", state: macOSPermissionStateMissing, required: false, wantWarning: true},
		// An unreadable privacy database must never fail an apply, because no
		// supported interface lets Terraform read it without Full Disk Access.
		{name: "unknown stays a warning", state: macOSPermissionStateUnknown, required: true, wantWarning: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stub := &stubMacOSPermissionManager{state: tt.state}
			resource := &MacOSPermissionResource{manager: stub}

			model := macOSPermissionTestModel()
			model.Required = types.BoolValue(tt.required)

			var diags diag.Diagnostics
			result := resource.verify(t.Context(), model, &diags)

			if diags.HasError() != tt.wantError {
				t.Fatalf("HasError got %t, want %t: %s", diags.HasError(), tt.wantError, diagnosticsError(diags))
			}
			if got := diags.WarningsCount() > 0; got != tt.wantWarning {
				t.Fatalf("warnings got %t, want %t", got, tt.wantWarning)
			}
			if result.Granted.ValueBool() != tt.wantGranted {
				t.Fatalf("granted got %t, want %t", result.Granted.ValueBool(), tt.wantGranted)
			}
			if result.State.ValueString() != tt.state {
				t.Fatalf("state got %q, want %q", result.State.ValueString(), tt.state)
			}
			if result.SettingsURL.ValueString() != "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_Accessibility" {
				t.Fatalf("settings_url got %q", result.SettingsURL.ValueString())
			}
		})
	}
}

func TestMacOSPermissionVerifyOpensSettingsOnlyWhenMissing(t *testing.T) {
	t.Parallel()

	stub := &stubMacOSPermissionManager{state: macOSPermissionStateGranted}
	res := &MacOSPermissionResource{manager: stub}
	model := macOSPermissionTestModel()
	model.OpenSettingsWhenMissing = types.BoolValue(true)

	var diags diag.Diagnostics
	res.verify(t.Context(), model, &diags)
	if len(stub.opened) != 0 {
		t.Fatalf("expected a granted permission not to open System Settings, got %#v", stub.opened)
	}

	stub.state = macOSPermissionStateMissing
	res.verify(t.Context(), model, &diags)
	if len(stub.opened) != 1 || !strings.HasSuffix(stub.opened[0], "?Privacy_Accessibility") {
		t.Fatalf("got %#v", stub.opened)
	}
}

func TestCLIMacOSPermissionManagerReadsAuthValue(t *testing.T) {
	t.Parallel()

	service, err := lookupMacOSPermissionService("accessibility")
	if err != nil {
		t.Fatalf("lookupMacOSPermissionService: %s", err)
	}
	spec := macOSPermissionSpec{Service: service, Client: "org.hammerspoon.Hammerspoon"}

	tests := []struct {
		name   string
		output string
		err    error
		want   string
	}{
		{name: "allowed", output: "2\n", want: macOSPermissionStateGranted},
		{name: "denied", output: "0\n", want: macOSPermissionStateDenied},
		{name: "undecided", output: "1\n", want: macOSPermissionStateMissing},
		{name: "no row", output: "\n", want: macOSPermissionStateMissing},
		{name: "unreadable", err: fmt.Errorf("authorization denied"), want: macOSPermissionStateUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calledArgs []string
			manager := &CLIMacOSPermissionManager{
				sqlitePath: "sqlite3",
				run: func(ctx context.Context, command string, args ...string) ([]byte, error) {
					calledArgs = args
					if tt.err != nil {
						return nil, tt.err
					}
					return []byte(tt.output), nil
				},
			}

			state, err := manager.PermissionState(t.Context(), spec)
			if err != nil {
				t.Fatalf("PermissionState: %s", err)
			}
			if state != tt.want {
				t.Fatalf("got %q, want %q", state, tt.want)
			}
			if len(calledArgs) > 0 {
				query := calledArgs[len(calledArgs)-1]
				if !strings.Contains(query, "'kTCCServiceAccessibility'") || !strings.Contains(query, "'org.hammerspoon.Hammerspoon'") {
					t.Fatalf("unexpected query %q", query)
				}
				if calledArgs[0] != "-readonly" {
					t.Fatalf("expected a read-only query, got %#v", calledArgs)
				}
			}
		})
	}
}

func TestMacOSPermissionVerifyReportsManagerFailure(t *testing.T) {
	t.Parallel()

	res := &MacOSPermissionResource{manager: &stubMacOSPermissionManager{stateErr: fmt.Errorf("boom")}}

	var diags diag.Diagnostics
	res.verify(t.Context(), macOSPermissionTestModel(), &diags)
	if !diags.HasError() {
		t.Fatal("expected a manager failure to surface as an error")
	}
	if !strings.Contains(diagnosticsError(diags).Error(), "boom") {
		t.Fatalf("got %q", diagnosticsError(diags))
	}
}
