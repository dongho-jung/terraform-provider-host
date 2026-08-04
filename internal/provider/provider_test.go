package provider

import (
	"os"
	osuser "os/user"
	"strings"
	"testing"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
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

func TestProviderSudoPreauthIsOptional(t *testing.T) {
	t.Parallel()

	provider := New("test")()
	var resp frameworkprovider.SchemaResponse
	provider.Schema(t.Context(), frameworkprovider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	attribute, ok := resp.Schema.Attributes["sudo_preauth"].(providerschema.BoolAttribute)
	if !ok {
		t.Fatalf("sudo_preauth attribute has type %T", resp.Schema.Attributes["sudo_preauth"])
	}
	if !attribute.Optional || attribute.Required {
		t.Fatalf("sudo_preauth Optional=%t Required=%t, want optional only", attribute.Optional, attribute.Required)
	}
}

func TestProviderDoesNotRegisterAURHelperResource(t *testing.T) {
	t.Parallel()

	provider := New("test")()
	for _, factory := range provider.Resources(t.Context()) {
		var resp frameworkresource.MetadataResponse
		factory().Metadata(
			t.Context(),
			frameworkresource.MetadataRequest{ProviderTypeName: "host"},
			&resp,
		)
		if resp.TypeName == "host_aur_helper" {
			t.Fatal("host_aur_helper resource must not be registered")
		}
	}
}

// configureHostProvider runs the provider's Configure step against config.
func configureHostProvider(t *testing.T, config HostProviderModel) frameworkprovider.ConfigureResponse {
	t.Helper()

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

	return resp
}

func TestResolveDefaultHostTargetUser(t *testing.T) {
	for name, testCase := range map[string]struct {
		sudoUser string
		expected string
	}{
		"sudo run credits the original login user": {
			sudoUser: "alice",
			expected: "alice",
		},
		// SUDO_USER always names the invoking user, so root there means root
		// ran sudo. Defaulting to root's home is never what was meant.
		"root invoking sudo has no default": {
			sudoUser: "root",
			expected: "",
		},
		"unusable name has no default": {
			sudoUser: "not a valid user",
			expected: "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SUDO_USER", testCase.sudoUser)

			if got := resolveDefaultHostTargetUser(); got != testCase.expected {
				t.Fatalf("expected %q, got %q", testCase.expected, got)
			}
		})
	}

	t.Run("plain run credits the current user", func(t *testing.T) {
		t.Setenv("SUDO_USER", "")

		current, err := osuser.Current()
		if err != nil {
			t.Skipf("current user is unavailable: %s", err)
		}
		expected := current.Username
		if expected == "root" {
			expected = ""
		}

		if got := resolveDefaultHostTargetUser(); got != expected {
			t.Fatalf("expected %q, got %q", expected, got)
		}
	})
}

func TestProviderRequiresTargetUserWhenItCannotBeDefaulted(t *testing.T) {
	// Stand in for a root run, the only case that resolves to no default.
	hostDefaultTargetUser = func() string { return "" }
	t.Cleanup(func() { hostDefaultTargetUser = resolveDefaultHostTargetUser })

	for _, test := range []struct {
		name   string
		config HostProviderModel
		want   string
	}{
		{
			name: "helper without target user",
			config: HostProviderModel{
				AURHelper: types.StringValue("yay"),
			},
			want: "aur_helper requires target_user",
		},
		{
			name: "cleanup without target user",
			config: HostProviderModel{
				AURRemoveMakeDependencies: types.BoolValue(true),
			},
			want: "AUR cleanup requires target_user",
		},
		{
			name: "home_dir without target user",
			config: HostProviderModel{
				HomeDir: types.StringValue("/home/alice"),
			},
			want: "home_dir requires target_user",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := configureHostProvider(t, test.config)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected diagnostic containing %q", test.want)
			}
			if got := resp.Diagnostics.Errors()[0].Summary(); !strings.Contains(got, test.want) {
				t.Fatalf("diagnostic summary %q does not contain %q", got, test.want)
			}
		})
	}
}

func TestProviderConfiguresWithoutArguments(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a root run has no default target user")
	}

	// The whole point of the defaults: an empty provider block is enough.
	resp := configureHostProvider(t, HostProviderModel{SudoPreauth: types.BoolValue(false)})
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics.Errors())
	}

	data, ok := resp.ResourceData.(HostProviderData)
	if !ok {
		t.Fatalf("provider data has type %T", resp.ResourceData)
	}
	if data.TargetUser != resolveDefaultHostTargetUser() {
		t.Fatalf("expected target_user %q, got %q", resolveDefaultHostTargetUser(), data.TargetUser)
	}
	if data.HomeDir == "" || data.RuntimeDir == "" {
		t.Fatalf("expected a user context, got home %q and runtime %q", data.HomeDir, data.RuntimeDir)
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
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resp := configureHostProvider(t, test.config)
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
