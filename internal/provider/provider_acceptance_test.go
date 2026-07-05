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
  target_user = %q
}`, current.Username),
			},
		},
	})
}
