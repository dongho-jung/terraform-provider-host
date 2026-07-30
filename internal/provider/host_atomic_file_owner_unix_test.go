//go:build !windows

package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteHostFileAtomicallyPreservesOwnership(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "managed")
	if err := os.WriteFile(path, []byte("before"), 0o640); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat initial file: %v", err)
	}
	beforeUID, beforeGID, err := hostSystemFileNumericOwnership(beforeInfo)
	if err != nil {
		t.Fatalf("read initial file ownership: %v", err)
	}

	if err := writeHostFileAtomically(path, []byte("after"), 0o640); err != nil {
		t.Fatalf("write file atomically: %v", err)
	}

	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat updated file: %v", err)
	}
	afterUID, afterGID, err := hostSystemFileNumericOwnership(afterInfo)
	if err != nil {
		t.Fatalf("read updated file ownership: %v", err)
	}

	if beforeUID != afterUID || beforeGID != afterGID {
		t.Fatalf(
			"ownership changed from %s:%s to %s:%s",
			beforeUID,
			beforeGID,
			afterUID,
			afterGID,
		)
	}
}
