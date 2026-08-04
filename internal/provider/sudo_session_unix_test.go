//go:build !windows

package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// writeStubSudo installs a stand-in for sudo that records every invocation and
// exits with the requested status for `-n -v` timestamp checks.
func writeStubSudo(t *testing.T, timestampValid bool) (sudoPath string, logPath string) {
	t.Helper()

	directory := t.TempDir()
	sudoPath = filepath.Join(directory, "sudo")
	logPath = filepath.Join(directory, "invocations")

	timestampExit := "1"
	if timestampValid {
		timestampExit = "0"
	}

	script := "#!/bin/sh\n" +
		// printf rather than echo, whose builtin would swallow sudo's -n flag.
		"printf '%s\\n' \"$*\" >>\"" + logPath + "\"\n" +
		"if [ \"$1\" = \"-n\" ]; then exit " + timestampExit + "; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(sudoPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write stub sudo: %s", err)
	}

	return sudoPath, logPath
}

func readStubSudoInvocations(t *testing.T, logPath string) []string {
	t.Helper()

	content, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read stub sudo invocations: %s", err)
	}

	return strings.Split(strings.TrimSpace(string(content)), "\n")
}

func TestAuthenticateHostSudoReusesAValidTimestamp(t *testing.T) {
	sudoPath, logPath := writeStubSudo(t, true)
	// A prompt would have to open this path, so pointing it at a missing file
	// proves the valid timestamp short-circuits before any terminal access.
	hostSudoTerminalPath = filepath.Join(t.TempDir(), "absent-terminal")
	t.Cleanup(func() { hostSudoTerminalPath = "/dev/tty" })

	if err := authenticateHostSudo(t.Context(), sudoPath, "install /etc/keyd/default.conf"); err != nil {
		t.Fatalf("expected a valid timestamp to authenticate silently: %s", err)
	}

	invocations := readStubSudoInvocations(t, logPath)
	if len(invocations) != 1 || invocations[0] != "-n -v" {
		t.Fatalf("expected only a timestamp check, got %#v", invocations)
	}
}

func TestAuthenticateHostSudoPromptsOnTheTerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	sudoPath, logPath := writeStubSudo(t, false)
	terminalPath := filepath.Join(t.TempDir(), "terminal")
	if err := os.WriteFile(terminalPath, nil, 0o600); err != nil {
		t.Fatalf("write stand-in terminal: %s", err)
	}
	hostSudoTerminalPath = terminalPath
	t.Cleanup(func() { hostSudoTerminalPath = "/dev/tty" })

	if err := authenticateHostSudo(t.Context(), sudoPath, "install /etc/sudoers.d/vpn"); err != nil {
		t.Fatalf("authenticate: %s", err)
	}

	// The timestamp is checked twice on purpose: once before taking the prompt
	// lock and once after, so a concurrent operation that authenticated while
	// this one waited does not cause a second prompt.
	invocations := readStubSudoInvocations(t, logPath)
	if len(invocations) != 3 {
		t.Fatalf("expected two timestamp checks followed by a prompt, got %#v", invocations)
	}
	if invocations[0] != "-n -v" || invocations[1] != "-n -v" {
		t.Fatalf("expected the prompt to be guarded by timestamp checks, got %#v", invocations)
	}
	if invocations[2] != "-p "+hostSudoPrompt+" -v" {
		t.Fatalf("expected the prompt to be overridden, got %q", invocations[2])
	}

	written, err := os.ReadFile(terminalPath)
	if err != nil {
		t.Fatalf("read stand-in terminal: %s", err)
	}
	transcript := string(written)
	if !strings.Contains(transcript, "SUDO PASSWORD REQUIRED") {
		t.Fatalf("expected the banner on the terminal, got %q", transcript)
	}
	if !strings.Contains(transcript, "install /etc/sudoers.d/vpn") {
		t.Fatalf("expected the operation on the terminal, got %q", transcript)
	}
	if !strings.Contains(transcript, "sudo authenticated, continuing.") {
		t.Fatalf("expected a completion notice on the terminal, got %q", transcript)
	}
}

func TestAuthenticateHostSudoReportsAMissingTerminal(t *testing.T) {
	sudoPath, _ := writeStubSudo(t, false)
	hostSudoTerminalPath = filepath.Join(t.TempDir(), "absent-terminal")
	t.Cleanup(func() { hostSudoTerminalPath = "/dev/tty" })

	err := authenticateHostSudo(t.Context(), sudoPath, "install /etc/sudoers.d/vpn")
	if err == nil {
		t.Fatal("expected an error when no terminal is available")
	}
	if !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("expected the error to explain the missing terminal, got %q", err)
	}
	if !strings.Contains(err.Error(), "install /etc/sudoers.d/vpn") {
		t.Fatalf("expected the error to name the operation, got %q", err)
	}
}

func TestAuthenticateHostSudoRequiresSudo(t *testing.T) {
	err := authenticateHostSudo(t.Context(), "", "install /etc/sudoers.d/vpn")
	if err == nil {
		t.Fatal("expected an error when sudo is unavailable")
	}
	if !strings.Contains(err.Error(), "sudo was not found") {
		t.Fatalf("expected the error to explain the missing sudo, got %q", err)
	}
}

// configureHostProviderWithStubSudo runs the provider's Configure step with a
// stub sudo on PATH and no reachable terminal, so pre-authentication is forced
// down its failure path without touching the real one.
func configureHostProviderWithStubSudo(t *testing.T, config HostProviderModel) (frameworkprovider.ConfigureResponse, string) {
	t.Helper()

	sudoPath, logPath := writeStubSudo(t, false)
	t.Setenv("PATH", filepath.Dir(sudoPath))
	hostSudoTerminalPath = filepath.Join(t.TempDir(), "absent-terminal")
	t.Cleanup(func() { hostSudoTerminalPath = "/dev/tty" })

	ctx := t.Context()
	hostProvider := New("test")()

	var schemaResp frameworkprovider.SchemaResponse
	hostProvider.Schema(ctx, frameworkprovider.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}

	configState := tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	if diags := configState.Set(ctx, &config); diags.HasError() {
		t.Fatalf("encode provider config: %v", diags)
	}

	var resp frameworkprovider.ConfigureResponse
	hostProvider.Configure(ctx, frameworkprovider.ConfigureRequest{
		Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: configState.Raw},
	}, &resp)

	return resp, logPath
}

func TestProviderSudoPreauthWarnsRatherThanFailingWithoutATerminal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root never authenticates through sudo")
	}

	resp, logPath := configureHostProviderWithStubSudo(t, HostProviderModel{
		TargetUser:  types.StringNull(),
		HomeDir:     types.StringNull(),
		RuntimeDir:  types.StringNull(),
		SudoPreauth: types.BoolValue(true),
	})

	// A host without a controlling terminal, such as CI, must still configure.
	if resp.Diagnostics.HasError() {
		t.Fatalf("expected pre-authentication failure to be a warning: %v", resp.Diagnostics.Errors())
	}
	if resp.Diagnostics.WarningsCount() != 1 {
		t.Fatalf("expected exactly one warning, got %v", resp.Diagnostics.Warnings())
	}
	if summary := resp.Diagnostics.Warnings()[0].Summary(); summary != "sudo pre-authentication skipped" {
		t.Fatalf("unexpected warning summary %q", summary)
	}
	if resp.ResourceData == nil {
		t.Fatal("expected the provider to stay configured after a skipped pre-authentication")
	}

	if invocations := readStubSudoInvocations(t, logPath); len(invocations) == 0 {
		t.Fatal("expected pre-authentication to attempt a sudo timestamp check")
	}
}

func TestProviderSudoPreauthDefaultsToEnabled(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root never authenticates through sudo")
	}

	_, logPath := configureHostProviderWithStubSudo(t, HostProviderModel{
		TargetUser:  types.StringNull(),
		HomeDir:     types.StringNull(),
		RuntimeDir:  types.StringNull(),
		SudoPreauth: types.BoolNull(),
	})

	if invocations := readStubSudoInvocations(t, logPath); len(invocations) == 0 {
		t.Fatal("expected an unset sudo_preauth to authenticate up front")
	}
}

func TestProviderSudoPreauthCanBeDisabled(t *testing.T) {
	resp, logPath := configureHostProviderWithStubSudo(t, HostProviderModel{
		TargetUser:  types.StringNull(),
		HomeDir:     types.StringNull(),
		RuntimeDir:  types.StringNull(),
		SudoPreauth: types.BoolValue(false),
	})

	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.Diagnostics.WarningsCount() != 0 {
		t.Fatalf("expected no warnings, got %v", resp.Diagnostics.Warnings())
	}
	if invocations := readStubSudoInvocations(t, logPath); len(invocations) != 0 {
		t.Fatalf("expected sudo to stay untouched when sudo_preauth is false, got %#v", invocations)
	}
}

func TestPreauthenticateHostSudoSkipsWhenAlreadyRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("this case only applies when the tests run as root")
	}

	if err := preauthenticateHostSudo(t.Context(), ""); err != nil {
		t.Fatalf("expected root to need no authentication: %s", err)
	}
}

func TestPreauthenticateHostSudoRequiresSudo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root never authenticates through sudo")
	}

	err := preauthenticateHostSudo(t.Context(), "")
	if err == nil {
		t.Fatal("expected an error when sudo is unavailable")
	}
	if !strings.Contains(err.Error(), "sudo was not found") {
		t.Fatalf("expected the error to explain the missing sudo, got %q", err)
	}
}
