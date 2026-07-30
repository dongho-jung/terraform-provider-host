package provider

import (
	"fmt"
	osuser "os/user"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccProviderConfig(t *testing.T) {
	current, err := osuser.Current()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`provider "host" {
  target_user = %q
}`, current.Username),
			},
		},
	})
}

func TestAccProviderConfigAllowsSystemOnlyScope(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
provider "host" {}

resource "host_system_file" "scope_test" {
  destination = "/etc/terraform-provider-host-system-scope-test"
  content     = "system scope test\n"
}
`,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccUserScopedResourceRequiresTargetUser(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
provider "host" {}

resource "host_dir" "test" {
  path = "/tmp/terraform-provider-host-user-scope-test"
}
`,
				ExpectError: regexp.MustCompile(`User-scoped Host object requires target_user`),
			},
		},
	})
}

func TestAccProviderUserDirectoriesRequireTargetUser(t *testing.T) {
	for _, test := range []struct {
		name        string
		argument    string
		expectError string
	}{
		{name: "home_dir", argument: `home_dir = "/tmp/terraform-provider-host-home"`, expectError: "home_dir requires target_user"},
		{name: "runtime_dir", argument: `runtime_dir = "/tmp/terraform-provider-host-runtime"`, expectError: "runtime_dir requires target_user"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: fmt.Sprintf(`
provider "host" {
  %s
}

resource "host_dir" "test" {
  path = "/tmp/terraform-provider-host-user-directory-test"
}
`, test.argument),
						ExpectError: regexp.MustCompile(test.expectError),
					},
				},
			})
		})
	}
}

func TestAccProviderConfigAllowsExplicitHomeDirForMissingTargetUser(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `provider "host" {
  target_user = "tfhostmissingtargetuser"
  home_dir    = "/tmp/tfhostmissingtargetuser"
}`,
			},
		},
	})
}
