package provider

import (
	"reflect"
	"testing"
)

func TestEnvironmentWithCLocaleReplacesExistingLocale(t *testing.T) {
	t.Parallel()

	got := environmentWithCLocale([]string{
		"PATH=/usr/bin",
		"LANG=ko_KR.UTF-8",
		"LC_ALL=en_US.UTF-8",
		"TERM=xterm",
	})
	want := []string{
		"PATH=/usr/bin",
		"TERM=xterm",
		"LC_ALL=C",
		"LANG=C",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment got %#v, want %#v", got, want)
	}
}
