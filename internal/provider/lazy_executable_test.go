package provider

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLazyExecutablePathRetriesNotFound(t *testing.T) {
	t.Parallel()

	resolver := newLazyExecutablePath("example-tool", "")
	var calls atomic.Int32
	resolver.lookPath = func(name string) (string, error) {
		if name != "example-tool" {
			t.Fatalf("lookup name got %q", name)
		}
		if calls.Add(1) == 1 {
			return "", errors.New("not found yet")
		}
		return "/usr/bin/example-tool", nil
	}

	if _, err := resolver.resolve("example operation"); err == nil {
		t.Fatal("first lookup unexpectedly succeeded")
	}
	path, err := resolver.resolve("example operation")
	if err != nil {
		t.Fatalf("second lookup: %s", err)
	}
	if path != "/usr/bin/example-tool" {
		t.Fatalf("path got %q", path)
	}
	if calls.Load() != 2 {
		t.Fatalf("lookup calls got %d, want 2", calls.Load())
	}

	if _, err := resolver.resolve("example operation"); err != nil {
		t.Fatalf("cached lookup: %s", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("successful path was not cached; calls got %d", calls.Load())
	}
}

func TestLazyExecutablePathCachesSuccessConcurrently(t *testing.T) {
	t.Parallel()

	resolver := newLazyExecutablePath("example-tool", "")
	var calls atomic.Int32
	resolver.lookPath = func(string) (string, error) {
		calls.Add(1)
		return "/usr/bin/example-tool", nil
	}

	const goroutines = 32
	var waitGroup sync.WaitGroup
	waitGroup.Add(goroutines)
	errors := make(chan error, goroutines)
	for range goroutines {
		go func() {
			defer waitGroup.Done()
			path, err := resolver.resolve("concurrent operation")
			if err != nil {
				errors <- err
				return
			}
			if path != "/usr/bin/example-tool" {
				errors <- fmt.Errorf("unexpected executable path: %s", path)
			}
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("lookup calls got %d, want 1", calls.Load())
	}
}

func TestLazyExecutablePathUsesConfiguredPath(t *testing.T) {
	t.Parallel()

	resolver := newLazyExecutablePath("example-tool", "/configured/example-tool")
	resolver.lookPath = func(string) (string, error) {
		t.Fatal("configured path unexpectedly performed lookup")
		return "", nil
	}

	path, err := resolver.resolve("example operation")
	if err != nil {
		t.Fatalf("resolve configured path: %s", err)
	}
	if path != "/configured/example-tool" {
		t.Fatalf("path got %q", path)
	}
}
