package provider

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type recordingHostSudoersValidator struct {
	calls int
}

func (v *recordingHostSudoersValidator) Validate(context.Context, []byte) error {
	v.calls++
	return nil
}

func TestRenderHostSudoersStructuredRuleIsStable(t *testing.T) {
	commands, diags := types.SetValueFrom(t.Context(), types.StringType, []string{
		"/usr/local/bin/vpn-up",
		"/usr/local/bin/vpn-down",
	})
	if diags.HasError() {
		t.Fatalf("commands set: %v", diags)
	}
	rendered, err := renderHostSudoersRule(t.Context(), HostSudoersRuleResourceModel{
		User:       types.StringValue("dongho"),
		Commands:   commands,
		RunAs:      types.StringValue("root"),
		NoPassword: types.BoolValue(true),
	})
	if err != nil {
		t.Fatalf("render structured rule: %s", err)
	}
	want := "dongho ALL=(root) NOPASSWD: /usr/local/bin/vpn-down \"\", /usr/local/bin/vpn-up \"\"\n"
	if string(rendered) != want {
		t.Fatalf("rendered got %q, want %q", rendered, want)
	}
}

func TestHostSystemRootGroupForOS(t *testing.T) {
	tests := map[string]string{
		"linux":   "root",
		"windows": "root",
		"darwin":  "wheel",
		"freebsd": "wheel",
	}
	for goos, want := range tests {
		if got := hostSystemRootGroupForOS(goos); got != want {
			t.Errorf("root group on %s got %q, want %q", goos, got, want)
		}
	}
}

func TestRenderHostSudoersRuleRejectsUnsafeInputs(t *testing.T) {
	unsafeCommands, _ := types.SetValueFrom(t.Context(), types.StringType, []string{"relative"})
	tests := []HostSudoersRuleResourceModel{
		{
			User:       types.StringValue("dongho"),
			Commands:   unsafeCommands,
			RunAs:      types.StringValue("root"),
			NoPassword: types.BoolValue(true),
		},
	}
	for _, model := range tests {
		if _, err := renderHostSudoersRule(t.Context(), model); err == nil {
			t.Fatal("expected invalid sudoers rule")
		}
	}
}

func TestValidateHostSudoersRuleNameAndCommand(t *testing.T) {
	for _, name := range []string{"vpn", "vpn_access", "vpn-access-2"} {
		if err := validateHostSudoersRuleName(name); err != nil {
			t.Fatalf("valid name %q: %s", name, err)
		}
	}
	for _, name := range []string{"", "../vpn", "vpn.conf", "vpn~", "-vpn"} {
		if err := validateHostSudoersRuleName(name); err == nil {
			t.Fatalf("expected invalid name %q", name)
		}
	}
	for _, command := range []string{"relative", "/usr/bin/tool arg", "/usr/bin/a,/usr/bin/b", "/usr/bin/../bin/true", "/usr/bin/tool*", "/usr/bin/tool?"} {
		if err := validateHostSudoersCommand(command); err == nil {
			t.Fatalf("expected invalid command %q", command)
		}
	}
	for _, principal := range []string{"dongho", "build-user", "service.account"} {
		if err := validateHostSudoersPrincipal(principal); err != nil {
			t.Fatalf("valid principal %q: %s", principal, err)
		}
	}
	for _, principal := range []string{"ALL", "ADMINS", "Dongho", "%wheel", "dongho,root", "user!root", "#0", "user(root)"} {
		if err := validateHostSudoersPrincipal(principal); err == nil {
			t.Fatalf("expected invalid principal %q", principal)
		}
	}
}

func TestValidateHostSudoersCommandFileRequiresProtectedRootExecutable(t *testing.T) {
	trusted, err := trustedHostSystemExecutable("test")
	if err != nil {
		t.Skipf("trusted test executable unavailable: %s", err)
	}
	if err := validateHostSudoersCommandFile(trusted); err != nil {
		t.Fatalf("trusted command rejected: %s", err)
	}

	unsafe := filepath.Join(t.TempDir(), "user-wrapper")
	if err := os.WriteFile(unsafe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write unsafe wrapper: %s", err)
	}
	if err := validateHostSudoersCommandFile(unsafe); err == nil || !strings.Contains(err.Error(), "root-owned") {
		t.Fatalf("user-owned command error got %v", err)
	}
}

func TestHostSudoersCreateRejectsUnsafeCommandBeforePrivilegedCalls(t *testing.T) {
	ctx := t.Context()
	unsafe := filepath.Join(t.TempDir(), "user-wrapper")
	if err := os.WriteFile(unsafe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write unsafe wrapper: %s", err)
	}
	commands, diags := types.SetValueFrom(ctx, types.StringType, []string{unsafe})
	if diags.HasError() {
		t.Fatalf("commands: %v", diags)
	}
	manager := &recordingHostSystemFileManager{}
	validator := &recordingHostSudoersValidator{}
	r := &HostSudoersRuleResource{manager: manager, validator: validator, targetUser: "dongho"}
	var schemaResp frameworkresource.SchemaResponse
	r.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}
	model := HostSudoersRuleResourceModel{
		ID:                     types.StringValue("vpn"),
		Name:                   types.StringValue("vpn"),
		User:                   types.StringValue("dongho"),
		Commands:               commands,
		RunAs:                  types.StringValue("root"),
		NoPassword:             types.BoolValue(true),
		RenderedContent:        types.StringUnknown(),
		Path:                   types.StringUnknown(),
		ChecksumSHA256:         types.StringUnknown(),
		DeployedChecksumSHA256: types.StringUnknown(),
		Mode:                   types.StringUnknown(),
		Owner:                  types.StringUnknown(),
		Group:                  types.StringUnknown(),
		AdoptExisting:          types.BoolValue(false),
		DeleteOnDestroy:        types.BoolValue(true),
	}
	plan := tfsdk.Plan{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatalf("encode plan: %v", diags)
	}
	var resp frameworkresource.CreateResponse
	r.Create(ctx, frameworkresource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Summary(), "Unsafe sudoers command") {
		t.Fatalf("unsafe command diagnostics: %v", resp.Diagnostics)
	}
	if manager.fileCalls != 0 || len(manager.installCalls) != 0 || validator.calls != 0 {
		t.Fatalf("unsafe command reached privileged backend: file=%d install=%d visudo=%d", manager.fileCalls, len(manager.installCalls), validator.calls)
	}
	var updateResp frameworkresource.UpdateResponse
	r.Update(ctx, frameworkresource.UpdateRequest{Plan: plan}, &updateResp)
	if !updateResp.Diagnostics.HasError() || !strings.Contains(updateResp.Diagnostics.Errors()[0].Summary(), "Unsafe sudoers command") {
		t.Fatalf("unsafe update diagnostics: %v", updateResp.Diagnostics)
	}
	if manager.fileCalls != 0 || len(manager.installCalls) != 0 || validator.calls != 0 {
		t.Fatalf("unsafe update reached privileged backend: file=%d install=%d visudo=%d", manager.fileCalls, len(manager.installCalls), validator.calls)
	}
}

func TestHostSudoersReadWarnsOnUnsafeCommandWithoutBlockingStateRefresh(t *testing.T) {
	ctx := t.Context()
	unsafe := filepath.Join(t.TempDir(), "user-wrapper")
	if err := os.WriteFile(unsafe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write unsafe wrapper: %s", err)
	}
	commands, diags := types.SetValueFrom(ctx, types.StringType, []string{unsafe})
	if diags.HasError() {
		t.Fatalf("commands: %v", diags)
	}
	rendered := "dongho ALL=(root) NOPASSWD: " + unsafe + " \"\"\n"
	checksum := hostSystemFileChecksum([]byte(rendered))
	manager := &recordingHostSystemFileManager{
		exists: true,
		status: HostSystemFileStatus{
			Destination:    hostSudoersRulePath("vpn"),
			ChecksumSHA256: checksum,
			Mode:           0o440,
			Owner:          "root",
			Group:          hostSystemRootGroup(),
		},
	}
	r := &HostSudoersRuleResource{manager: manager, validator: &recordingHostSudoersValidator{}, targetUser: "dongho"}
	var schemaResp frameworkresource.SchemaResponse
	r.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}
	model := HostSudoersRuleResourceModel{
		ID:                     types.StringValue("vpn"),
		Name:                   types.StringValue("vpn"),
		User:                   types.StringValue("dongho"),
		Commands:               commands,
		RunAs:                  types.StringValue("root"),
		NoPassword:             types.BoolValue(true),
		RenderedContent:        types.StringValue(rendered),
		Path:                   types.StringValue(hostSudoersRulePath("vpn")),
		ChecksumSHA256:         types.StringValue(checksum),
		DeployedChecksumSHA256: types.StringValue(checksum),
		Mode:                   types.StringValue("0440"),
		Owner:                  types.StringValue("root"),
		Group:                  types.StringValue(hostSystemRootGroup()),
		AdoptExisting:          types.BoolValue(false),
		DeleteOnDestroy:        types.BoolValue(true),
	}
	state := tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	if diags := state.Set(ctx, &model); diags.HasError() {
		t.Fatalf("encode state: %v", diags)
	}
	resp := frameworkresource.ReadResponse{State: state}
	r.Read(ctx, frameworkresource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() || len(resp.Diagnostics.Warnings()) != 1 {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}
	if manager.fileCalls != 1 {
		t.Fatalf("read file calls got %d, want 1", manager.fileCalls)
	}
}

func TestHostSudoersRuleRequiresProviderTargetUser(t *testing.T) {
	resource := &HostSudoersRuleResource{targetUser: "dongho"}
	if err := resource.validateTargetUser(HostSudoersRuleResourceModel{User: types.StringValue("dongho")}); err != nil {
		t.Fatalf("target user rejected: %s", err)
	}
	if err := resource.validateTargetUser(HostSudoersRuleResourceModel{User: types.StringValue("other")}); err == nil {
		t.Fatal("non-target sudoers user was accepted")
	}
}

func TestCLIHostSudoersValidator(t *testing.T) {
	visudoPath, err := exec.LookPath("visudo")
	if err != nil {
		t.Skip("visudo is not available")
	}
	validator := NewCLIHostSudoersValidator(visudoPath)
	if err := validator.Validate(t.Context(), []byte("dongho ALL=(root) NOPASSWD: /usr/bin/true\n")); err != nil {
		t.Fatalf("valid sudoers content: %s", err)
	}
	err = validator.Validate(t.Context(), []byte("this is not valid sudoers syntax\n"))
	if err == nil || !strings.Contains(err.Error(), "visudo validation failed") {
		t.Fatalf("invalid sudoers error got %v", err)
	}
}

func TestHostSudoersRuleSchemaValid(t *testing.T) {
	resource := NewHostSudoersRuleResource()
	var response frameworkresource.SchemaResponse
	resource.Schema(t.Context(), frameworkresource.SchemaRequest{}, &response)
	if diags := response.Schema.ValidateImplementation(t.Context()); diags.HasError() {
		t.Fatalf("invalid host_sudoers_rule schema: %v", diags)
	}
}
