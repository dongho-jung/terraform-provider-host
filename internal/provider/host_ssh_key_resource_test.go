package provider

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestHostSSHKeyConfigureAllowsMissingSSHKeygen(t *testing.T) {
	t.Parallel()

	resource := &HostSSHKeyResource{}
	var resp frameworkresource.ConfigureResponse
	resource.Configure(t.Context(), frameworkresource.ConfigureRequest{
		ProviderData: HostProviderData{HomeDir: t.TempDir()},
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics)
	}
	if resource.manager == nil {
		t.Fatal("configure did not install lazy SSH key manager")
	}
}

func TestSSHKeyStatusFromPublicKey(t *testing.T) {
	t.Parallel()

	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDFfQ6Q3lgEA3d0+qMprwL6f3JUPr5SwJ2sUvJHcYg+Z test-comment"

	status, err := sshKeyStatusFromPublicKey("/tmp/id_ed25519", "/tmp/id_ed25519.pub", publicKey)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if status.Type != hostSSHKeyTypeEd25519 {
		t.Fatalf("type got %q, want %q", status.Type, hostSSHKeyTypeEd25519)
	}
	if status.Comment != "test-comment" {
		t.Fatalf("comment got %q, want test-comment", status.Comment)
	}
	if !strings.HasPrefix(status.FingerprintSHA256, "SHA256:") {
		t.Fatalf("fingerprint got %q, want SHA256 prefix", status.FingerprintSHA256)
	}
}

func TestValidateSSHKeyBits(t *testing.T) {
	t.Parallel()

	if err := validateSSHKeyBits(hostSSHKeyTypeEd25519, 256); err == nil {
		t.Fatal("expected ed25519 bits error")
	}
	if err := validateSSHKeyBits(hostSSHKeyTypeRSA, 1024); err == nil {
		t.Fatal("expected small rsa bits error")
	}
	if err := validateSSHKeyBits(hostSSHKeyTypeECDSA, 4096); err == nil {
		t.Fatal("expected invalid ecdsa bits error")
	}
	if err := validateSSHKeyBits(hostSSHKeyTypeRSA, 4096); err != nil {
		t.Fatalf("unexpected rsa bits error: %s", err)
	}
}

func TestCLISSHKeyManagerEnsureKey(t *testing.T) {
	t.Parallel()

	sshKeygenPath, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not available")
	}

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519_test")
	manager := NewCLISSHKeyManager(sshKeygenPath)

	status, err := manager.EnsureKey(t.Context(), HostSSHKeySpec{
		Path:    keyPath,
		Type:    hostSSHKeyTypeEd25519,
		Comment: "terraform-provider-host-test",
	})
	if err != nil {
		t.Fatalf("ensure key: %s", err)
	}
	if status.Type != hostSSHKeyTypeEd25519 {
		t.Fatalf("type got %q, want %q", status.Type, hostSSHKeyTypeEd25519)
	}
	if status.Comment != "terraform-provider-host-test" {
		t.Fatalf("comment got %q, want terraform-provider-host-test", status.Comment)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("private key missing: %s", err)
	}
	if _, err := os.Stat(keyPath + ".pub"); err != nil {
		t.Fatalf("public key missing: %s", err)
	}

	if err := manager.DeleteKey(t.Context(), keyPath); err != nil {
		t.Fatalf("delete key: %s", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("private key still exists or stat failed with unexpected error: %v", err)
	}
}

func TestCLISSHKeyManagerRetriesSSHKeygenInstalledAfterConfigure(t *testing.T) {
	sshKeygenPath, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not available")
	}

	resolver := newLazyExecutablePath("ssh-keygen", "")
	var lookups atomic.Int32
	resolver.lookPath = func(string) (string, error) {
		if lookups.Add(1) == 1 {
			return "", errors.New("ssh-keygen not installed yet")
		}
		return sshKeygenPath, nil
	}
	manager := &CLISSHKeyManager{sshKeygenExecutable: resolver}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519_lazy")
	spec := HostSSHKeySpec{
		Path:    keyPath,
		Type:    hostSSHKeyTypeEd25519,
		Comment: "terraform-provider-host-lazy-test",
	}

	if _, err := manager.EnsureKey(t.Context(), spec); err == nil {
		t.Fatal("ensure before ssh-keygen installation unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "SSH key generation") || !strings.Contains(err.Error(), `executable "ssh-keygen"`) {
		t.Fatalf("unexpected operation-time error: %s", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("failed lookup created private key: %v", err)
	}

	status, err := manager.EnsureKey(t.Context(), spec)
	if err != nil {
		t.Fatalf("ensure after ssh-keygen installation: %s", err)
	}
	if status.Type != hostSSHKeyTypeEd25519 {
		t.Fatalf("type got %q, want %q", status.Type, hostSSHKeyTypeEd25519)
	}
	if lookups.Load() != 2 {
		t.Fatalf("lookups got %d, want 2", lookups.Load())
	}
}
