package provider

import "testing"

func TestParsePacmanPackageLine(t *testing.T) {
	t.Parallel()

	name, version, ok := parsePacmanPackageLine("git 2.50.1-1")
	if !ok {
		t.Fatal("expected package line to parse")
	}
	if name != "git" || version != "2.50.1-1" {
		t.Fatalf("got name=%q version=%q", name, version)
	}
}

func TestParsePacmanUpgradeLine(t *testing.T) {
	t.Parallel()

	name, version, ok := parsePacmanUpgradeLine("git 2.50.1-1 -> 2.51.0-1")
	if !ok {
		t.Fatal("expected upgrade line to parse")
	}
	if name != "git" || version != "2.51.0-1" {
		t.Fatalf("got name=%q version=%q", name, version)
	}
}

func TestParsePacmanInfoValue(t *testing.T) {
	t.Parallel()

	got := parsePacmanInfoValue("Repository      : extra\nName            : git\nVersion         : 2.50.1-1\n", "Version")
	if got != "2.50.1-1" {
		t.Fatalf("got %q", got)
	}
}
