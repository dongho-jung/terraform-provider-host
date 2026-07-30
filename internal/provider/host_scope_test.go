package provider

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

func TestRequireHostUserScope(t *testing.T) {
	t.Parallel()

	var diagnostics diag.Diagnostics
	if !requireHostUserScope(HostProviderData{TargetUser: "alice"}, "host_file", &diagnostics) {
		t.Fatal("configured user scope was rejected")
	}
	if diagnostics.HasError() {
		t.Fatalf("configured user scope diagnostics: %v", diagnostics)
	}

	if requireHostUserScope(HostProviderData{}, "host_file", &diagnostics) {
		t.Fatal("missing user scope was accepted")
	}
	if !diagnostics.HasError() {
		t.Fatal("expected an error diagnostic")
	}
	detail := diagnostics.Errors()[0].Detail()
	for _, want := range []string{"`host_file` is user-scoped", "Set `target_user`", "System-scoped Host objects"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("diagnostic detail %q does not contain %q", detail, want)
		}
	}
}
