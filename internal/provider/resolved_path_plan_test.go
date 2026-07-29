package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestResolvedPathChanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		state   types.String
		plan    string
		changed bool
		wantErr bool
	}{
		{name: "same", state: types.StringValue("/Users/alice/projects"), plan: "/Users/alice/projects"},
		{name: "changed", state: types.StringValue("/Users/alice/old"), plan: "/Users/alice/projects", changed: true},
		{name: "null", state: types.StringNull(), plan: "/Users/alice/projects"},
		{name: "unknown", state: types.StringUnknown(), plan: "/Users/alice/projects"},
		{name: "empty", state: types.StringValue(""), plan: "/Users/alice/projects", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed, err := resolvedPathChanged(tt.state, tt.plan)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error got %v, wantErr %t", err, tt.wantErr)
			}
			if changed != tt.changed {
				t.Fatalf("changed got %t, want %t", changed, tt.changed)
			}
		})
	}
}
