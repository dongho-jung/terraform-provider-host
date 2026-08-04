package provider

import (
	"sync"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

var sudoPlanWarningOnce sync.Once

func addSudoPrivilegeWarningOnce(diags *diag.Diagnostics) {
	shouldAdd := false
	sudoPlanWarningOnce.Do(func() {
		shouldAdd = true
	})
	if !shouldAdd {
		return
	}

	diags.AddWarning(
		"sudo authentication may be required",
		"At least one planned host operation may need sudo. The provider authenticates through `sudo` on the controlling terminal when no valid timestamp exists, and reuses that timestamp while it remains valid. Set `sudo_preauth = true` on the provider to authenticate once before the run starts, instead of wherever the first privileged operation lands in Terraform's output. Running `sudo -v` beforehand works too.",
	)
}
