package provider

import (
	"testing"
)

func TestHostUserStringSetEmptySliceIsKnownEmptySet(t *testing.T) {
	t.Parallel()

	got, diags := hostUserStringSet(t.Context(), nil)
	if diags.HasError() {
		t.Fatalf("hostUserStringSet diagnostics: %s", diags.Errors()[0].Summary())
	}
	if got.IsNull() {
		t.Fatal("expected empty set, got null")
	}
	if got.IsUnknown() {
		t.Fatal("expected empty set, got unknown")
	}
	if len(got.Elements()) != 0 {
		t.Fatalf("got %d elements, want 0", len(got.Elements()))
	}
}
