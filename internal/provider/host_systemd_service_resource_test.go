package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSystemdServiceShouldRestart(t *testing.T) {
	t.Parallel()

	running := func(trigger types.String) HostSystemdServiceResourceModel {
		return HostSystemdServiceResourceModel{
			Running:        types.BoolValue(true),
			RestartTrigger: trigger,
		}
	}

	tests := []struct {
		name  string
		state HostSystemdServiceResourceModel
		plan  HostSystemdServiceResourceModel
		want  bool
	}{
		{
			name:  "changed trigger",
			state: running(types.StringValue("old")),
			plan:  running(types.StringValue("new")),
			want:  true,
		},
		{
			name:  "new trigger",
			state: running(types.StringNull()),
			plan:  running(types.StringValue("new")),
			want:  true,
		},
		{
			name:  "same trigger",
			state: running(types.StringValue("same")),
			plan:  running(types.StringValue("same")),
			want:  false,
		},
		{
			name: "service will be started",
			state: HostSystemdServiceResourceModel{
				Running:        types.BoolValue(false),
				RestartTrigger: types.StringValue("old"),
			},
			plan: running(types.StringValue("new")),
			want: false,
		},
		{
			name:  "trigger removed",
			state: running(types.StringValue("old")),
			plan:  running(types.StringNull()),
			want:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := systemdServiceShouldRestart(test.state, test.plan); got != test.want {
				t.Fatalf("systemdServiceShouldRestart() = %t, want %t", got, test.want)
			}
		})
	}
}
