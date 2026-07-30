package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

func requireHostUserScope(data HostProviderData, terraformType string, diagnostics *diag.Diagnostics) bool {
	if data.TargetUser != "" {
		return true
	}

	diagnostics.AddError(
		"User-scoped Host object requires target_user",
		fmt.Sprintf(
			"`%s` is user-scoped, but this provider configuration has no user context. Set `target_user` on this provider (and optionally `home_dir` or `runtime_dir`), or use an aliased Host provider configured for that user. System-scoped Host objects can use a provider without `target_user`.",
			terraformType,
		),
	)
	return false
}
