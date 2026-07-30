package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestResourceSchemaVersionsDoNotRegress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		resource       resource.Resource
		minimumVersion int64
	}{
		{
			name:           "host_package_dnf",
			resource:       &DNFPackageResource{},
			minimumVersion: 2,
		},
		{
			name:           "host_file_block",
			resource:       &HostFileBlockResource{},
			minimumVersion: 2,
		},
		{
			name:           "host_file",
			resource:       &HostFileResource{},
			minimumVersion: 3,
		},
		{
			name:           "host_link",
			resource:       &HostLinkResource{},
			minimumVersion: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var resp resource.SchemaResponse
			test.resource.Schema(t.Context(), resource.SchemaRequest{}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
			}
			if resp.Schema.Version < test.minimumVersion {
				t.Fatalf(
					"schema version regressed to %d; released states require at least %d",
					resp.Schema.Version,
					test.minimumVersion,
				)
			}
		})
	}
}
