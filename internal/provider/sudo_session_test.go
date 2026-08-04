package provider

import (
	"strings"
	"testing"
)

func TestHostSudoReason(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		name     string
		args     []string
		expected string
	}{
		"command without arguments": {
			name:     "/usr/bin/visudo",
			expected: "/usr/bin/visudo",
		},
		"command with arguments": {
			name:     "/usr/bin/test",
			args:     []string{"-L", "/etc/sudoers.d/vpn"},
			expected: "/usr/bin/test -L /etc/sudoers.d/vpn",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if reason := hostSudoReason(testCase.name, testCase.args...); reason != testCase.expected {
				t.Fatalf("expected reason %q, got %q", testCase.expected, reason)
			}
		})
	}
}

func TestHostSudoBannerAnnouncesTheOperation(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	banner := hostSudoBanner("/usr/bin/test -L /etc/sudoers.d/vpn")

	if !strings.HasPrefix(banner, "\a") {
		t.Fatalf("expected the banner to open with a bell, got %q", banner)
	}
	if !strings.Contains(banner, "SUDO PASSWORD REQUIRED") {
		t.Fatalf("expected an explicit heading, got %q", banner)
	}
	if !strings.Contains(banner, "/usr/bin/test -L /etc/sudoers.d/vpn") {
		t.Fatalf("expected the banner to name the operation, got %q", banner)
	}
	if !strings.Contains(banner, strings.Repeat("=", hostSudoBannerWidth)) {
		t.Fatalf("expected a full-width rule, got %q", banner)
	}
}

func TestHostSudoTerminalStyleHonorsPlainTerminals(t *testing.T) {
	for name, testCase := range map[string]struct {
		environment map[string]string
		styled      bool
	}{
		"color terminal": {
			environment: map[string]string{"TERM": "xterm-256color"},
			styled:      true,
		},
		"NO_COLOR set": {
			environment: map[string]string{"TERM": "xterm-256color", "NO_COLOR": "1"},
		},
		"dumb terminal": {
			environment: map[string]string{"TERM": "dumb"},
		},
		"no terminal type": {
			environment: map[string]string{"TERM": ""},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// hostSudoTerminalStyle treats an empty NO_COLOR as unset, so this
			// clears any value inherited from the developer's environment.
			t.Setenv("NO_COLOR", "")
			for key, value := range testCase.environment {
				t.Setenv(key, value)
			}

			emphasis, reset := hostSudoTerminalStyle()
			if testCase.styled {
				if emphasis == "" || reset == "" {
					t.Fatalf("expected styling, got emphasis %q and reset %q", emphasis, reset)
				}
				return
			}
			if emphasis != "" || reset != "" {
				t.Fatalf("expected no styling, got emphasis %q and reset %q", emphasis, reset)
			}
		})
	}
}

func TestHostSudoNoticeIsSetApartFromTerraformOutput(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	notice := hostSudoNotice("sudo authenticated, continuing")

	if !strings.HasPrefix(notice, "\n") || !strings.HasSuffix(notice, "\n\n") {
		t.Fatalf("expected the notice to be padded with blank lines, got %q", notice)
	}
	if !strings.Contains(notice, "sudo authenticated, continuing.") {
		t.Fatalf("expected the notice to carry the message, got %q", notice)
	}
}
