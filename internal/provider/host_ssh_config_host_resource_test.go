package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestRenderHostSSHConfigBlock(t *testing.T) {
	t.Parallel()

	identitiesOnly := true
	forwardAgent := false
	got := renderHostSSHConfigBlock(hostSSHConfigHostSpec{
		Host:           "github.com",
		HostName:       "github.com",
		User:           "git",
		Port:           22,
		IdentityFile:   "~/.ssh/id_ed25519_github",
		IdentitiesOnly: &identitiesOnly,
		ForwardAgent:   &forwardAgent,
		ProxyJump:      "bastion",
		ExtraOptions: map[string]string{
			"ServerAliveInterval": "30",
		},
	})
	want := strings.Join([]string{
		"Host github.com",
		"  HostName github.com",
		"  User git",
		"  Port 22",
		"  IdentityFile ~/.ssh/id_ed25519_github",
		"  IdentitiesOnly yes",
		"  ForwardAgent no",
		"  ProxyJump bastion",
		"  ServerAliveInterval 30",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("rendered block got:\n%s\nwant:\n%s", got, want)
	}
}

func TestHostSSHConfigManagedBlockPreservesUnmanagedContent(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config")
	initial := strings.Join([]string{
		"Host existing",
		"  HostName existing.example.com",
		"",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("write initial config: %s", err)
	}

	spec := hostSSHConfigHostSpec{
		ID:   "test-block",
		Host: "github.com",
		User: "git",
	}
	rendered := renderHostSSHConfigBlock(spec)
	if err := upsertHostSSHConfigManagedBlock(configPath, spec.ID, spec.Host, rendered, false); err != nil {
		t.Fatalf("upsert config: %s", err)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %s", err)
	}
	text := string(content)
	if !strings.Contains(text, "Host existing\n  HostName existing.example.com\n") {
		t.Fatalf("unmanaged content was not preserved:\n%s", text)
	}
	if !strings.Contains(text, hostSSHConfigBlockBeginMarker(spec.ID)) || !strings.Contains(text, "Host github.com\n  User git\n") {
		t.Fatalf("managed block missing:\n%s", text)
	}

	got, exists, err := readHostSSHConfigManagedBlock(configPath, spec.ID)
	if err != nil {
		t.Fatalf("read managed block: %s", err)
	}
	if !exists {
		t.Fatal("managed block not found")
	}
	if got != rendered {
		t.Fatalf("managed block got:\n%s\nwant:\n%s", got, rendered)
	}

	if err := removeHostSSHConfigManagedBlock(configPath, spec.ID); err != nil {
		t.Fatalf("remove managed block: %s", err)
	}
	content, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after remove: %s", err)
	}
	text = string(content)
	if strings.Contains(text, "Host github.com") {
		t.Fatalf("managed host still exists:\n%s", text)
	}
	if !strings.Contains(text, "Host existing") {
		t.Fatalf("unmanaged content missing after remove:\n%s", text)
	}
}

func TestHostSSHConfigManagedBlockRejectsUnmanagedDuplicate(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host github.com\n  User git\n"), 0o600); err != nil {
		t.Fatalf("write initial config: %s", err)
	}

	spec := hostSSHConfigHostSpec{
		ID:   "test-block",
		Host: "github.com",
		User: "git",
	}
	err := upsertHostSSHConfigManagedBlock(configPath, spec.ID, spec.Host, renderHostSSHConfigBlock(spec), false)
	if err == nil {
		t.Fatal("expected duplicate host error")
	}
	if !strings.Contains(err.Error(), "already contains unmanaged Host") {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestHostSSHConfigManagedBlockAdoptsUnmanagedDuplicate(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config")
	initial := strings.Join([]string{
		"Host github.com",
		"  AddKeysToAgent yes",
		"  IdentityFile ~/.ssh/id_ed25519",
		"",
		"Host bastion",
		"  User ubuntu",
		"",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("write initial config: %s", err)
	}

	spec := hostSSHConfigHostSpec{
		ID:           "test-block",
		Host:         "github.com",
		User:         "git",
		ExtraOptions: map[string]string{"AddKeysToAgent": "yes"},
	}
	if err := upsertHostSSHConfigManagedBlock(configPath, spec.ID, spec.Host, renderHostSSHConfigBlock(spec), true); err != nil {
		t.Fatalf("adopt config: %s", err)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %s", err)
	}
	text := string(content)
	if !strings.Contains(text, hostSSHConfigBlockBeginMarker(spec.ID)) {
		t.Fatalf("managed marker missing:\n%s", text)
	}
	if !strings.Contains(text, "Host bastion\n  User ubuntu\n") {
		t.Fatalf("unrelated host block missing:\n%s", text)
	}
	if strings.Contains(text, "Host github.com\n  AddKeysToAgent yes\n  IdentityFile ~/.ssh/id_ed25519\n\nHost github.com") {
		t.Fatalf("unmanaged duplicate was not replaced:\n%s", text)
	}
}

func TestReadUnmanagedSSHConfigHostBlock(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config")
	initial := strings.Join([]string{
		"Host github.com",
		"  AddKeysToAgent yes",
		"  IdentityFile ~/.ssh/id_ed25519",
		"",
		"Host bastion",
		"  User ubuntu",
		"",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("write initial config: %s", err)
	}

	got, exists, err := readUnmanagedSSHConfigHostBlock(configPath, "github.com")
	if err != nil {
		t.Fatalf("read unmanaged block: %s", err)
	}
	if !exists {
		t.Fatal("unmanaged block was not found")
	}
	want := "Host github.com\n  AddKeysToAgent yes\n  IdentityFile ~/.ssh/id_ed25519\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseHostSSHConfigHostImportID(t *testing.T) {
	t.Parallel()

	configPath, host, err := parseHostSSHConfigHostImportID("github.com")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if configPath != defaultSSHConfigPath || host != "github.com" {
		t.Fatalf("got %q %q", configPath, host)
	}

	configPath, host, err = parseHostSSHConfigHostImportID("/tmp/ssh_config,bastion")
	if err != nil {
		t.Fatalf("unexpected explicit path error: %s", err)
	}
	if configPath != "/tmp/ssh_config" || host != "bastion" {
		t.Fatalf("got %q %q", configPath, host)
	}
}

func TestHostSSHConfigHostSpecFromModel(t *testing.T) {
	t.Parallel()

	model := HostSSHConfigHostResourceModel{
		ConfigPath:     types.StringValue("~/.ssh/config"),
		Host:           types.StringValue("github.com"),
		HostName:       types.StringValue("github.com"),
		User:           types.StringValue("git"),
		IdentityFile:   types.StringValue("~/.ssh/id_ed25519_github"),
		IdentitiesOnly: types.BoolValue(true),
	}
	spec, diags := hostSSHConfigHostSpecFromModelForHome(t.Context(), model, "/Users/dongho")
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsError(diags))
	}
	if spec.Host != "github.com" || spec.User != "git" || spec.IdentitiesOnly == nil || !*spec.IdentitiesOnly {
		t.Fatalf("unexpected spec: %#v", spec)
	}
	if spec.IdentityFileResolved == "" {
		t.Fatal("identity_file_resolved should be set")
	}
}
