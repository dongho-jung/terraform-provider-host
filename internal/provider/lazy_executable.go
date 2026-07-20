package provider

import (
	"fmt"
	"os/exec"
	"sync"
)

// lazyExecutablePath resolves an executable when an operation needs it. A
// successful lookup is stable for the provider process, while a failed lookup
// is intentionally retried so an earlier package resource can install the
// executable during the same Terraform apply.
type lazyExecutablePath struct {
	mu           sync.Mutex
	name         string
	resolvedPath string
	lookPath     func(string) (string, error)
}

func newLazyExecutablePath(name string, configuredPath string) *lazyExecutablePath {
	return &lazyExecutablePath{
		name:         name,
		resolvedPath: configuredPath,
		lookPath:     exec.LookPath,
	}
}

func (r *lazyExecutablePath) resolve(operation string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.resolvedPath != "" {
		return r.resolvedPath, nil
	}

	path, err := r.lookPath(r.name)
	if err != nil || path == "" {
		if err != nil {
			return "", fmt.Errorf("%s requires executable %q in PATH, but it was not found; install it before this operation: %w", operation, r.name, err)
		}
		return "", fmt.Errorf("%s requires executable %q in PATH, but lookup returned an empty path; install it before this operation", operation, r.name)
	}

	r.resolvedPath = path
	return path, nil
}

func (r *lazyExecutablePath) tryResolve() (string, bool) {
	path, err := r.resolve("operation")
	return path, err == nil
}
