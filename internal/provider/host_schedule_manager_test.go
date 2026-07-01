package provider

import (
	"strings"
	"testing"
)

func TestValidateCronExpressionAllowsCronOrSemantics(t *testing.T) {
	t.Parallel()

	if err := validateCronExpression("0 3 1 * 1"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestCronExpressionFromEvery(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"1m":  "* * * * *",
		"15m": "*/15 * * * *",
		"1h":  "0 * * * *",
		"6h":  "0 */6 * * *",
		"24h": "0 0 * * *",
	}

	for input, want := range tests {
		got, err := cronExpressionFromEvery(input)
		if err != nil {
			t.Fatalf("cronExpressionFromEvery(%q): %s", input, err)
		}
		if got != want {
			t.Fatalf("cronExpressionFromEvery(%q) got %q, want %q", input, got, want)
		}
	}
}

func TestCronExpressionFromEveryRejectsInexactDuration(t *testing.T) {
	t.Parallel()

	_, err := cronExpressionFromEvery("90m")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cannot be represented exactly") {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestRenderHostScheduleCronEntry(t *testing.T) {
	t.Parallel()

	entry, err := renderHostScheduleCronEntry(
		HostScheduleSpec{
			ID:       "0123456789abcdef",
			Command:  "echo hello",
			Schedule: "*/30 * * * *",
			Shell:    "/bin/sh",
			Enabled:  true,
		},
		HostScheduleStatus{
			ID:         "0123456789abcdef",
			ScriptPath: "/tmp/provider schedules/run.sh",
		},
	)
	if err != nil {
		t.Fatalf("render cron entry: %s", err)
	}

	want := []string{
		"# terraform-provider-host schedule 0123456789abcdef",
		"*/30 * * * * '/tmp/provider schedules/run.sh'",
	}
	for i := range want {
		if entry[i] != want[i] {
			t.Fatalf("entry[%d] got %q, want %q", i, entry[i], want[i])
		}
	}
}

func TestFilterHostScheduleCronEntry(t *testing.T) {
	t.Parallel()

	lines := []string{
		"SHELL=/bin/zsh",
		"# terraform-provider-host schedule 0123456789abcdef",
		"*/30 * * * * /tmp/run.sh",
		"0 9 * * * /usr/bin/true",
	}

	got := filterHostScheduleCronEntry(lines, "0123456789abcdef", "/tmp/run.sh")
	want := []string{
		"SHELL=/bin/zsh",
		"0 9 * * * /usr/bin/true",
	}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestRenderHostScheduleScript(t *testing.T) {
	t.Parallel()

	script, err := renderHostScheduleScript(HostScheduleSpec{
		ID:         "0123456789abcdef",
		Command:    "echo '$HELLO'",
		Schedule:   "0 * * * *",
		Shell:      "/bin/zsh",
		StdoutPath: "/tmp/stdout.log",
		StderrPath: "/tmp/stderr.log",
		Environment: map[string]string{
			"HELLO": "hello world",
		},
	})
	if err != nil {
		t.Fatalf("render script: %s", err)
	}

	for _, want := range []string{
		"#!/bin/zsh\n",
		"exec >> '/tmp/stdout.log'\n",
		"exec 2>> '/tmp/stderr.log'\n",
		"export HELLO='hello world'\n",
		"echo '$HELLO'\n",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("expected script to contain %q:\n%s", want, script)
		}
	}
}
