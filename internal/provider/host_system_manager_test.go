package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSystemSetupTimezone(t *testing.T) {
	t.Parallel()

	got, err := parseSystemSetupTimezone("Time Zone: America/Los_Angeles\n")
	if err != nil {
		t.Fatalf("parseSystemSetupTimezone: %s", err)
	}
	if got != "America/Los_Angeles" {
		t.Fatalf("got %q", got)
	}
}

func TestParseSystemdEnabled(t *testing.T) {
	t.Parallel()

	if !parseSystemdEnabled("enabled\n") {
		t.Fatal("expected enabled output to parse true")
	}
	if parseSystemdEnabled("disabled\n") {
		t.Fatal("expected disabled output to parse false")
	}
}

func TestParseSystemdActive(t *testing.T) {
	t.Parallel()

	if !parseSystemdActive("active\n") {
		t.Fatal("expected active output to parse true")
	}
	if parseSystemdActive("inactive\n") {
		t.Fatal("expected inactive output to parse false")
	}
}

func TestParseSystemdServiceShow(t *testing.T) {
	t.Parallel()

	status, err := parseSystemdServiceShow(
		"docker.service",
		"ActiveState=active\nLoadState=loaded\nUnitFileState=enabled\n",
	)
	if err != nil {
		t.Fatalf("parse service show: %s", err)
	}
	if !status.Exists || !status.Enabled || !status.Running {
		t.Fatalf("unexpected status: %#v", status)
	}

	missing, err := parseSystemdServiceShow(
		"missing.service",
		"LoadState=not-found\nActiveState=inactive\nUnitFileState=\n",
	)
	if err != nil {
		t.Fatalf("parse missing service show: %s", err)
	}
	if missing.Exists || missing.Enabled || missing.Running {
		t.Fatalf("unexpected missing status: %#v", missing)
	}
}

func TestParseSystemdServiceShowRejectsIncompleteOutput(t *testing.T) {
	t.Parallel()

	if _, err := parseSystemdServiceShow("docker.service", "LoadState=loaded\n"); err == nil {
		t.Fatal("expected incomplete systemctl output to fail")
	}
}

func TestCLISystemdServiceManagerReadsStatusWithOneCommand(t *testing.T) {
	t.Parallel()

	systemctlPath := filepath.Join(t.TempDir(), "systemctl")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "${0}.log"
printf '%s\n' 'LoadState=loaded' 'UnitFileState=enabled' 'ActiveState=active'
`
	if err := os.WriteFile(systemctlPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write mock systemctl: %s", err)
	}
	manager := NewCLISystemdServiceManager(systemctlPath, "")
	status, err := manager.ServiceStatus(t.Context(), "docker.service")
	if err != nil {
		t.Fatalf("service status: %s", err)
	}
	if !status.Exists || !status.Enabled || !status.Running {
		t.Fatalf("unexpected service status: %#v", status)
	}
	log, err := os.ReadFile(systemctlPath + ".log")
	if err != nil {
		t.Fatalf("read systemctl log: %s", err)
	}
	if got := len(splitNonEmptyLines(string(log))); got != 1 {
		t.Fatalf("systemctl calls got %d, want 1", got)
	}
	if !strings.Contains(string(log), "--property=LoadState") ||
		!strings.Contains(string(log), "--property=UnitFileState") ||
		!strings.Contains(string(log), "--property=ActiveState") {
		t.Fatalf("unexpected systemctl command: %s", log)
	}
}

func TestParseLocalectlLocale(t *testing.T) {
	t.Parallel()

	out := "System Locale: LANG=en_US.UTF-8\n    VC Keymap: us\n"
	got, err := parseLocalectlLocale(out)
	if err != nil {
		t.Fatalf("parseLocalectlLocale: %s", err)
	}
	if got != "en_US.UTF-8" {
		t.Fatalf("got %q", got)
	}
}

func TestParseLocalectlKeymap(t *testing.T) {
	t.Parallel()

	out := "System Locale: LANG=en_US.UTF-8\n    VC Keymap: us\n"
	got, err := parseLocalectlKeymap(out)
	if err != nil {
		t.Fatalf("parseLocalectlKeymap: %s", err)
	}
	if got != "us" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateHostHostname(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"workstation", "dev-1.example.com"} {
		if err := validateHostHostname(name); err != nil {
			t.Fatalf("expected hostname %q to be valid: %s", name, err)
		}
	}

	for _, name := range []string{"", " host", "host ", "bad host", "-bad", "bad-", "bad..host", "bad_host"} {
		if err := validateHostHostname(name); err == nil {
			t.Fatalf("expected hostname %q to be invalid", name)
		}
	}
}

func TestValidateHostTimezone(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"UTC", "America/Los_Angeles", "Asia/Seoul"} {
		if err := validateHostTimezone(name); err != nil {
			t.Fatalf("expected timezone %q to be valid: %s", name, err)
		}
	}

	for _, name := range []string{"", " UTC", "UTC ", "/UTC", "../UTC", "America Los_Angeles"} {
		if err := validateHostTimezone(name); err == nil {
			t.Fatalf("expected timezone %q to be invalid", name)
		}
	}
}

func TestValidateHostLocale(t *testing.T) {
	t.Parallel()

	for _, lang := range []string{"en_US.UTF-8", "ko_KR.UTF-8", "C.UTF-8"} {
		if err := validateHostLocale(lang); err != nil {
			t.Fatalf("expected locale %q to be valid: %s", lang, err)
		}
	}

	for _, lang := range []string{"", " en_US.UTF-8", "en_US.UTF-8 ", "en US.UTF-8"} {
		if err := validateHostLocale(lang); err == nil {
			t.Fatalf("expected locale %q to be invalid", lang)
		}
	}
}

func TestValidateHostKeymap(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"us", "jp106", "de-latin1"} {
		if err := validateHostKeymap(name); err != nil {
			t.Fatalf("expected keymap %q to be valid: %s", name, err)
		}
	}

	for _, name := range []string{"", " us", "us ", "bad keymap"} {
		if err := validateHostKeymap(name); err == nil {
			t.Fatalf("expected keymap %q to be invalid", name)
		}
	}
}

func TestValidateSystemdUnitName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"example.service", "backup.timer", "dev-data.mount", "getty@tty1.service"} {
		if err := validateSystemdUnitName(name); err != nil {
			t.Fatalf("expected unit %q to be valid: %s", name, err)
		}
	}

	for _, name := range []string{"", "example", " example.service", "example.service ", "bad/unit.service", "bad unit.service"} {
		if err := validateSystemdUnitName(name); err == nil {
			t.Fatalf("expected unit %q to be invalid", name)
		}
	}
}

func TestValidateSystemdServiceName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"sshd.service", "docker.service", "getty@tty1.service"} {
		if err := validateSystemdServiceName(name); err != nil {
			t.Fatalf("expected service %q to be valid: %s", name, err)
		}
	}

	for _, name := range []string{"", "sshd", " sshd.service", "sshd.service ", "bad/service.service", "bad service.service"} {
		if err := validateSystemdServiceName(name); err == nil {
			t.Fatalf("expected service %q to be invalid", name)
		}
	}
}
