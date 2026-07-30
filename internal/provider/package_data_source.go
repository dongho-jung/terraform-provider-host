package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func packageDataSourceName(value types.String) (string, error) {
	if value.IsNull() || value.IsUnknown() {
		return "", fmt.Errorf("name must be known")
	}
	name := value.ValueString()
	if err := validatePackageName(name); err != nil {
		return "", err
	}
	return name, nil
}

func packageVersionValue(version string) types.String {
	if version == "" {
		return types.StringNull()
	}
	return types.StringValue(version)
}
