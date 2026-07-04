package provider

import (
	"fmt"

	tfpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type resolvedPathFunc func(string) (string, error)

func requireReplaceIfResolvedPathChanged(
	req resource.ModifyPlanRequest,
	resp *resource.ModifyPlanResponse,
	attributePath tfpath.Path,
	statePath types.String,
	statePathResolved types.String,
	planPathResolved string,
	resolve resolvedPathFunc,
) {
	if req.State.Raw.IsNull() {
		return
	}

	currentPathResolved, ok, err := currentResolvedPath(statePath, statePathResolved, resolve)
	if err != nil {
		resp.Diagnostics.AddError("Invalid prior path state", err.Error())
		return
	}
	if !ok {
		return
	}
	if currentPathResolved != planPathResolved {
		resp.RequiresReplace = append(resp.RequiresReplace, attributePath)
	}
}

func currentResolvedPath(path types.String, pathResolved types.String, resolve resolvedPathFunc) (string, bool, error) {
	if !pathResolved.IsNull() && !pathResolved.IsUnknown() {
		value := pathResolved.ValueString()
		if value == "" {
			return "", false, fmt.Errorf("resolved path state must not be empty")
		}
		return value, true, nil
	}
	if path.IsNull() || path.IsUnknown() {
		return "", false, nil
	}
	value, err := resolve(path.ValueString())
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}
