package provider

import (
	"context"
	"testing"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"host": providerserver.NewProtocol6WithError(New("test")()),
}

func TestProviderMetadata(t *testing.T) {
	t.Parallel()

	provider := New("test")()

	var resp frameworkprovider.MetadataResponse
	provider.Metadata(context.Background(), frameworkprovider.MetadataRequest{}, &resp)

	if resp.TypeName != "host" {
		t.Fatalf("expected provider type name host, got %q", resp.TypeName)
	}

	if resp.Version != "test" {
		t.Fatalf("expected provider version test, got %q", resp.Version)
	}
}

func TestConfiguredExecutablePathUsesExplicitValue(t *testing.T) {
	t.Parallel()

	got, err := configuredExecutablePath("definitely-not-a-real-host-provider-tool", types.StringValue("/custom/tool"))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got != "/custom/tool" {
		t.Fatalf("got %q, want /custom/tool", got)
	}
}

func TestConfiguredExecutablePathReturnsEmptyWhenToolIsMissing(t *testing.T) {
	t.Parallel()

	got, err := configuredExecutablePath("definitely-not-a-real-host-provider-tool", types.StringNull())
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got != "" {
		t.Fatalf("got %q, want empty path", got)
	}
}
