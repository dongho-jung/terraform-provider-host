package provider

import (
	"fmt"
	osuser "os/user"
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
  target_user  = %q
  sudo_preauth = false
}`, current.Username),
			},
		},
	})
}

func TestAccProviderConfigPlansSystemScopedResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
provider "host" {
  sudo_preauth = false
}

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

// An empty provider block used to be system-only. target_user now defaults to
// whoever runs Terraform, so user-scoped resources work without naming them.
func TestAccProviderConfigDefaultsTargetUser(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
provider "host" {
  sudo_preauth = false
}

resource "host_dir" "test" {
  path = "/tmp/terraform-provider-host-user-scope-test"
}
`,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// home_dir and runtime_dir used to require an explicit target_user. They now
// attach to the defaulted one.
func TestAccProviderUserDirectoriesUseDefaultedTargetUser(t *testing.T) {
	for _, test := range []struct {
		name     string
		argument string
	}{
		{name: "home_dir", argument: `home_dir = "/tmp/terraform-provider-host-home"`},
		{name: "runtime_dir", argument: `runtime_dir = "/tmp/terraform-provider-host-runtime"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: fmt.Sprintf(`
provider "host" {
  sudo_preauth = false
  %s
}

resource "host_dir" "test" {
  path = "/tmp/terraform-provider-host-user-directory-test"
}
`, test.argument),
						PlanOnly:           true,
						ExpectNonEmptyPlan: true,
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
  target_user  = "tfhostmissingtargetuser"
  home_dir     = "/tmp/tfhostmissingtargetuser"
  sudo_preauth = false
}`,
			},
		},
	})
}
