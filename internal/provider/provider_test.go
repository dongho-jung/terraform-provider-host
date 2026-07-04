package provider

import (
	"context"
	"testing"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
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
	provider.Metadata(context.Background(), frameworkprovider.MetadataRequest{}, &resp)

	if resp.TypeName != "host" {
		t.Fatalf("expected provider type name host, got %q", resp.TypeName)
	}

	if resp.Version != "test" {
		t.Fatalf("expected provider version test, got %q", resp.Version)
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

	got, err := expandHostPathWithHome("~/projects", "/Users/alice")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got != "/Users/alice/projects" {
		t.Fatalf("got %q, want /Users/alice/projects", got)
	}
}

func TestExpandHostPathForHomeUsesCallHome(t *testing.T) {
	t.Parallel()

	first, err := expandHostPathForHome("~/projects", "/Users/alice")
	if err != nil {
		t.Fatalf("expand first home: %s", err)
	}
	second, err := expandHostPathForHome("~/projects", "/Users/bob")
	if err != nil {
		t.Fatalf("expand second home: %s", err)
	}

	if first != "/Users/alice/projects" {
		t.Fatalf("first got %q, want /Users/alice/projects", first)
	}
	if second != "/Users/bob/projects" {
		t.Fatalf("second got %q, want /Users/bob/projects", second)
	}
}
