package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestCurrentResolvedPathPrefersStoredResolvedPath(t *testing.T) {
	t.Parallel()

	got, ok, err := currentResolvedPath(
		types.StringValue("~/projects"),
		types.StringValue("/Users/alice/projects"),
		func(path string) (string, error) {
			return "", fmt.Errorf("resolve should not be called for %q", path)
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !ok {
		t.Fatalf("expected resolved path")
	}
	if got != "/Users/alice/projects" {
		t.Fatalf("got %q, want /Users/alice/projects", got)
	}
}

func TestCurrentResolvedPathFallsBackToRawPath(t *testing.T) {
	t.Parallel()

	got, ok, err := currentResolvedPath(
		types.StringValue("~/projects"),
		types.StringNull(),
		func(path string) (string, error) {
			return expandHostPathWithHome(path, "/Users/alice")
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !ok {
		t.Fatalf("expected resolved path")
	}
	if got != "/Users/alice/projects" {
		t.Fatalf("got %q, want /Users/alice/projects", got)
	}
}
