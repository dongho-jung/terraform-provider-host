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
		"At least one planned host operation may need sudo. Unless `sudo_preauth` is disabled, the provider authenticates once on the controlling terminal before the run starts and renews that timestamp until the run ends, so no prompt should interrupt this operation. Running `sudo -v` beforehand works too.",
	)
}
