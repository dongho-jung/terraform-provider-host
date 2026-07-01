package provider

import (
	"reflect"
	"testing"
)

func TestParseGetentGroup(t *testing.T) {
	t.Parallel()

	got, err := parseGetentGroup("wheel:x:10:dongho,deploy")
	if err != nil {
		t.Fatalf("parseGetentGroup: %s", err)
	}

	if got.Name != "wheel" || got.GID != "10" {
		t.Fatalf("got %#v", got)
	}
}

func TestParseGetentPasswd(t *testing.T) {
	t.Parallel()

	got, err := parseGetentPasswd("dongho:x:1000:1000:Dongho Jung,,,:/home/dongho:/bin/zsh")
	if err != nil {
		t.Fatalf("parseGetentPasswd: %s", err)
	}

	if got.Username != "dongho" ||
		got.UID != "1000" ||
		got.GID != "1000" ||
		got.FullName != "Dongho Jung" ||
		got.Home != "/home/dongho" ||
		got.Shell != "/bin/zsh" {
		t.Fatalf("got %#v", got)
	}
}

func TestParseIDGroupNamesSortsGroups(t *testing.T) {
	t.Parallel()

	got := parseIDGroupNames("staff admin everyone\n")
	want := []string{"admin", "everyone", "staff"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseDSCLScalarAttribute(t *testing.T) {
	t.Parallel()

	got, err := parseDSCLScalarAttribute("PrimaryGroupID: 20\n", "PrimaryGroupID")
	if err != nil {
		t.Fatalf("parseDSCLScalarAttribute: %s", err)
	}
	if got != "20" {
		t.Fatalf("got %q", got)
	}
}

func TestParseDSCLScalarAttributeMultiline(t *testing.T) {
	t.Parallel()

	got, err := parseDSCLScalarAttribute("RealName:\n Dongho Jung\n", "RealName")
	if err != nil {
		t.Fatalf("parseDSCLScalarAttribute: %s", err)
	}
	if got != "Dongho Jung" {
		t.Fatalf("got %q", got)
	}
}

func TestParseDSCLListIDs(t *testing.T) {
	t.Parallel()

	got := parseDSCLListIDs("root 0\nstaff 20\ndongho 501\n")
	for _, id := range []int{0, 20, 501} {
		if !got[id] {
			t.Fatalf("expected id %d in %#v", id, got)
		}
	}
}

func TestValidateHostUserSpecRejectsRelativePaths(t *testing.T) {
	t.Parallel()

	home := "relative"
	err := validateHostUserSpec(HostUserSpec{
		Username:   "deploy",
		Home:       &home,
		CreateHome: true,
	})
	if err == nil {
		t.Fatal("expected home validation error")
	}

	shell := "zsh"
	err = validateHostUserSpec(HostUserSpec{
		Username:   "deploy",
		Shell:      &shell,
		CreateHome: true,
	})
	if err == nil {
		t.Fatal("expected shell validation error")
	}
}

func TestSortedSetDifference(t *testing.T) {
	t.Parallel()

	got := sortedSetDifference(
		map[string]struct{}{"z": {}, "a": {}, "b": {}},
		map[string]struct{}{"b": {}},
	)
	want := []string{"a", "z"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
