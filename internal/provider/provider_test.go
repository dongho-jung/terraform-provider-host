package provider

import (
	"strings"
	"testing"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
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

func TestProviderAURHelperConfigurationIsOptional(t *testing.T) {
	t.Parallel()

	provider := New("test")()
	var resp frameworkprovider.SchemaResponse
	provider.Schema(t.Context(), frameworkprovider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	for _, name := range []string{"aur_helper", "aur_helper_package"} {
		attribute, ok := resp.Schema.Attributes[name].(providerschema.StringAttribute)
		if !ok {
			t.Fatalf("%s attribute has type %T", name, resp.Schema.Attributes[name])
		}
		if !attribute.Optional || attribute.Required {
			t.Fatalf("%s Optional=%t Required=%t, want optional only", name, attribute.Optional, attribute.Required)
		}
	}

	for _, name := range []string{"aur_remove_make_dependencies", "aur_clean_after"} {
		attribute, ok := resp.Schema.Attributes[name].(providerschema.BoolAttribute)
		if !ok {
			t.Fatalf("%s attribute has type %T", name, resp.Schema.Attributes[name])
		}
		if !attribute.Optional || attribute.Required {
			t.Fatalf("%s Optional=%t Required=%t, want optional only", name, attribute.Optional, attribute.Required)
		}
	}
}

func TestProviderRejectsInvalidAURHelperConfiguration(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		config HostProviderModel
		want   string
	}{
		{
			name: "helper without target user",
			config: HostProviderModel{
				TargetUser:       types.StringNull(),
				HomeDir:          types.StringNull(),
				RuntimeDir:       types.StringNull(),
				AURHelper:        types.StringValue("yay"),
				AURHelperPackage: types.StringNull(),
			},
			want: "aur_helper requires target_user",
		},
		{
			name: "package without helper",
			config: HostProviderModel{
				TargetUser:       types.StringNull(),
				HomeDir:          types.StringNull(),
				RuntimeDir:       types.StringNull(),
				AURHelper:        types.StringNull(),
				AURHelperPackage: types.StringValue("yay-bin"),
			},
			want: "aur_helper_package requires aur_helper",
		},
		{
			name: "unsupported helper",
			config: HostProviderModel{
				TargetUser:       types.StringValue("dongho"),
				HomeDir:          types.StringNull(),
				RuntimeDir:       types.StringNull(),
				AURHelper:        types.StringValue("pacaur"),
				AURHelperPackage: types.StringNull(),
			},
			want: "Invalid AUR helper configuration",
		},
		{
			name: "cleanup without target user",
			config: HostProviderModel{
				TargetUser:                types.StringNull(),
				HomeDir:                   types.StringNull(),
				RuntimeDir:                types.StringNull(),
				AURHelper:                 types.StringNull(),
				AURHelperPackage:          types.StringNull(),
				AURRemoveMakeDependencies: types.BoolValue(true),
				AURCleanAfter:             types.BoolNull(),
			},
			want: "AUR cleanup requires target_user",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

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
			if diags := configState.Set(ctx, &test.config); diags.HasError() {
				t.Fatalf("encode provider config: %v", diags)
			}

			var resp frameworkprovider.ConfigureResponse
			hostProvider.Configure(ctx, frameworkprovider.ConfigureRequest{
				Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: configState.Raw},
			}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected diagnostic containing %q", test.want)
			}
			if got := resp.Diagnostics.Errors()[0].Summary(); !strings.Contains(got, test.want) {
				t.Fatalf("diagnostic summary %q does not contain %q", got, test.want)
			}
		})
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
