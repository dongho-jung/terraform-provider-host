package provider

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

	status, err := manager.EnsureKey(context.Background(), HostSSHKeySpec{
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

	if err := manager.DeleteKey(context.Background(), keyPath); err != nil {
		t.Fatalf("delete key: %s", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("private key still exists or stat failed with unexpected error: %v", err)
	}
}
