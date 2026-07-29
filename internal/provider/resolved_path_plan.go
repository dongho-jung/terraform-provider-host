package provider

import (
	"fmt"

	tfpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func requireReplaceIfResolvedPathChanged(
	req resource.ModifyPlanRequest,
	resp *resource.ModifyPlanResponse,
	attributePath tfpath.Path,
	statePathResolved types.String,
	planPathResolved string,
) {
	if req.State.Raw.IsNull() {
		return
	}

	changed, err := resolvedPathChanged(statePathResolved, planPathResolved)
	if err != nil {
		resp.Diagnostics.AddError("Invalid prior path state", err.Error())
		return
	}
	if changed {
		resp.RequiresReplace = append(resp.RequiresReplace, attributePath)
	}
}

func resolvedPathChanged(statePathResolved types.String, planPathResolved string) (bool, error) {
	if statePathResolved.IsNull() || statePathResolved.IsUnknown() {
		return false, nil
	}
	value := statePathResolved.ValueString()
	if value == "" {
		return false, fmt.Errorf("resolved path state must not be empty")
	}
	return value != planPathResolved, nil
}
