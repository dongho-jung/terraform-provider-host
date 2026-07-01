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
		"At least one planned host operation may need sudo. During apply, the provider will authenticate through `sudo -v` in the current terminal when sudo is not already authenticated, reuse the sudo lease while it remains valid, and print reminders if Terraform status lines hide the prompt. You can also run `sudo -v` before `terraform apply`.",
	)
}
