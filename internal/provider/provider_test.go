package provider

import (
	"testing"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"host": providerserver.NewProtocol6WithError(New("test")()),
}

func TestProviderMetadata(t *testing.T) {
	t.Parallel()

	provider := New("test")()

	var resp frameworkprovider.MetadataResponse
	provider.Metadata(t.Context(), frameworkprovider.MetadataRequest{}, &resp)

	if resp.TypeName != "host" {
		t.Fatalf("expected provider type name host, got %q", resp.TypeName)
	}

	if resp.Version != "test" {
		t.Fatalf("expected provider version test, got %q", resp.Version)
	}
}

func TestProviderTargetUserIsOptional(t *testing.T) {
	t.Parallel()

	provider := New("test")()
	var resp frameworkprovider.SchemaResponse
	provider.Schema(t.Context(), frameworkprovider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	targetUser, ok := resp.Schema.Attributes["target_user"].(providerschema.StringAttribute)
	if !ok {
		t.Fatalf("target_user attribute has type %T", resp.Schema.Attributes["target_user"])
	}
	if !targetUser.Optional || targetUser.Required {
		t.Fatalf("target_user Optional=%t Required=%t, want optional only", targetUser.Optional, targetUser.Required)
	}
}

func TestExecutablePathReturnsEmptyWhenToolIsMissing(t *testing.T) {
	t.Parallel()

	got := executablePath("definitely-not-a-real-host-provider-tool")
	if got != "" {
		t.Fatalf("got %q, want empty path", got)
	}
}

func TestExpandHostPathWithConfiguredHome(t *testing.T) {
	t.Parallel()

	got, err := expandHostPathWithHome("~/projects", "/Users/dongho")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got != "/Users/dongho/projects" {
		t.Fatalf("got %q, want /Users/dongho/projects", got)
	}
}

func TestExpandHostPathWithHomeRequiresHome(t *testing.T) {
	t.Parallel()

	if _, err := expandHostPathWithHome("~/projects", ""); err == nil {
		t.Fatalf("expected empty home directory to fail")
	}
}

func TestValidateHostUserName(t *testing.T) {
	t.Parallel()

	for _, username := range []string{"dongho", "alice_1", "build-user"} {
		if err := validateHostUserName(username); err != nil {
			t.Fatalf("expected %q to be valid: %s", username, err)
		}
	}

	for _, username := range []string{"", " dongho", "dongho ", "bad/user", "bad:user", "-bad", "bad user"} {
		if err := validateHostUserName(username); err == nil {
			t.Fatalf("expected %q to be invalid", username)
		}
	}
}
