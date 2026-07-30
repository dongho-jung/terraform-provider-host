package provider

import (
	"errors"
	"strings"
	"testing"

	frameworkdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestHostDesktopSessionValidator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		target    string
		execution string
		active    string
		want      string
	}{
		{name: "matching user and session", target: "alice", execution: "alice", active: "alice"},
		{name: "different execution user", target: "alice", execution: "root", active: "alice", want: `running as local user "root"`},
		{name: "different active user", target: "alice", execution: "alice", active: "bob", want: `desktop session belongs to "bob"`},
		{name: "login window", target: "alice", execution: "alice", active: "root", want: "no macOS desktop session is active"},
		{name: "missing target", want: "target_user is not configured"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validator := &hostDesktopSessionValidator{
				currentUsername: func() (string, error) { return test.execution, nil },
				activeUsername:  func() (string, error) { return test.active, nil },
			}
			err := validator.Validate(test.target)
			if test.want == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestHostDesktopSessionValidatorPropagatesLookupErrors(t *testing.T) {
	t.Parallel()

	validator := &hostDesktopSessionValidator{
		currentUsername: func() (string, error) { return "", errors.New("current lookup failed") },
		activeUsername:  func() (string, error) { return "alice", nil },
	}
	if err := validator.Validate("alice"); err == nil || !strings.Contains(err.Error(), "current lookup failed") {
		t.Fatalf("Validate() error = %v", err)
	}

	validator.currentUsername = func() (string, error) { return "alice", nil }
	validator.activeUsername = func() (string, error) { return "", errors.New("console lookup failed") }
	if err := validator.Validate("alice"); err == nil || !strings.Contains(err.Error(), "console lookup failed") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRequireHostDesktopSessionAddsActionableDiagnostic(t *testing.T) {
	t.Parallel()

	validator := &hostDesktopSessionValidator{
		currentUsername: func() (string, error) { return "bob", nil },
		activeUsername:  func() (string, error) { return "bob", nil },
	}
	var diagnostics diag.Diagnostics
	if requireHostDesktopSession("alice", validator, &diagnostics) {
		t.Fatal("requireHostDesktopSession() returned true")
	}
	if !diagnostics.HasError() {
		t.Fatal("expected an error diagnostic")
	}
	detail := diagnostics.Errors()[0].Detail()
	for _, want := range []string{`Log in to the macOS desktop as "alice"`, "do not run Terraform through sudo", "terraform apply -target=<resource-address>"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("diagnostic detail %q does not contain %q", detail, want)
		}
	}
}

type rejectingHostDesktopSessionValidator struct{}

func (rejectingHostDesktopSessionValidator) Validate(targetUser string) error {
	return errors.New("desktop session rejected for test")
}

func TestMacOSSessionBoundObjectsValidateBeforePlanningOrReading(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	validator := rejectingHostDesktopSessionValidator{}
	audioManager := &fakeMacOSAudioDataSourceManager{}

	dock := &MacOSDockAppResource{item: MacOSDockItemResource{kind: macOSDockItemKindApp}}
	var dockConfigure frameworkresource.ConfigureResponse
	dock.Configure(ctx, frameworkresource.ConfigureRequest{ProviderData: HostProviderData{
		TargetUser:              "alice",
		HomeDir:                 "/Users/alice",
		RuntimeDir:              "/Users/alice/.local/state/terraform-provider-host",
		MacOSDockManager:        &fakeMacOSDockManager{},
		DesktopSessionValidator: validator,
	}}, &dockConfigure)
	if dockConfigure.Diagnostics.HasError() {
		t.Fatalf("Dock configure diagnostics: %v", dockConfigure.Diagnostics)
	}
	var dockPlan frameworkresource.ModifyPlanResponse
	dock.ModifyPlan(ctx, frameworkresource.ModifyPlanRequest{}, &dockPlan)
	assertDesktopSessionDiagnostic(t, dockPlan.Diagnostics)

	loginItem := &MacOSLoginItemResource{}
	var loginConfigure frameworkresource.ConfigureResponse
	loginItem.Configure(ctx, frameworkresource.ConfigureRequest{ProviderData: HostProviderData{
		TargetUser:              "alice",
		HomeDir:                 "/Users/alice",
		MacOSLoginItemManager:   &fakeMacOSLoginItemManager{items: map[string]MacOSLoginItemStatus{}},
		DesktopSessionValidator: validator,
	}}, &loginConfigure)
	if loginConfigure.Diagnostics.HasError() {
		t.Fatalf("Login Item configure diagnostics: %v", loginConfigure.Diagnostics)
	}
	var loginPlan frameworkresource.ModifyPlanResponse
	loginItem.ModifyPlan(ctx, frameworkresource.ModifyPlanRequest{}, &loginPlan)
	assertDesktopSessionDiagnostic(t, loginPlan.Diagnostics)

	audio := &MacOSAudioMultiOutputResource{}
	var audioConfigure frameworkresource.ConfigureResponse
	audio.Configure(ctx, frameworkresource.ConfigureRequest{ProviderData: HostProviderData{
		TargetUser:              "alice",
		MacOSAudioManager:       audioManager,
		DesktopSessionValidator: validator,
	}}, &audioConfigure)
	if audioConfigure.Diagnostics.HasError() {
		t.Fatalf("audio resource configure diagnostics: %v", audioConfigure.Diagnostics)
	}
	var audioPlan frameworkresource.ModifyPlanResponse
	audio.ModifyPlan(ctx, frameworkresource.ModifyPlanRequest{}, &audioPlan)
	assertDesktopSessionDiagnostic(t, audioPlan.Diagnostics)

	audioDevice := &MacOSAudioDeviceDataSource{}
	var dataSourceConfigure frameworkdatasource.ConfigureResponse
	audioDevice.Configure(ctx, frameworkdatasource.ConfigureRequest{ProviderData: HostProviderData{
		TargetUser:              "alice",
		MacOSAudioManager:       audioManager,
		DesktopSessionValidator: validator,
	}}, &dataSourceConfigure)
	if dataSourceConfigure.Diagnostics.HasError() {
		t.Fatalf("audio data source configure diagnostics: %v", dataSourceConfigure.Diagnostics)
	}
	var dataSourceRead frameworkdatasource.ReadResponse
	audioDevice.Read(ctx, frameworkdatasource.ReadRequest{}, &dataSourceRead)
	assertDesktopSessionDiagnostic(t, dataSourceRead.Diagnostics)
}

func assertDesktopSessionDiagnostic(t *testing.T, diagnostics diag.Diagnostics) {
	t.Helper()
	if !diagnostics.HasError() {
		t.Fatal("expected desktop session diagnostic")
	}
	if summary := diagnostics.Errors()[0].Summary(); summary != "Active target-user macOS desktop session required" {
		t.Fatalf("diagnostic summary = %q", summary)
	}
}
