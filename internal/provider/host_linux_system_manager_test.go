package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateHostSysctlKey(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"vm.swappiness", "net.ipv4.ip_forward", "kernel.domainname"} {
		if err := validateHostSysctlKey(key); err != nil {
			t.Fatalf("expected sysctl key %q to be valid: %s", key, err)
		}
	}

	for _, key := range []string{"", " vm.swappiness", "vm.swappiness ", "-bad", "bad key"} {
		if err := validateHostSysctlKey(key); err == nil {
			t.Fatalf("expected sysctl key %q to be invalid", key)
		}
	}
}

func TestSysctlManagedPath(t *testing.T) {
	t.Parallel()

	got := sysctlManagedPath("vm.swappiness")
	if got != "/etc/sysctl.d/99-terraform-host-vm.swappiness.conf" {
		t.Fatalf("got %q", got)
	}
}

func TestFstabManagedEntryLifecycle(t *testing.T) {
	t.Parallel()

	entry := FstabEntry{
		Name:       "data",
		Device:     "UUID=1234",
		MountPoint: "/data",
		FSType:     "ext4",
		Options:    "defaults,noatime",
		Dump:       0,
		Pass:       2,
	}

	content := "# existing\n"
	content, err := upsertFstabManagedEntry(content, entry)
	if err != nil {
		t.Fatalf("upsertFstabManagedEntry: %s", err)
	}
	if !strings.Contains(content, "UUID=1234\t/data\text4\tdefaults,noatime\t0\t2") {
		t.Fatalf("managed entry not found in content: %q", content)
	}

	got, exists, err := readFstabManagedEntry(content, "data")
	if err != nil {
		t.Fatalf("readFstabManagedEntry: %s", err)
	}
	if !exists {
		t.Fatal("expected managed entry to exist")
	}
	if got != entry {
		t.Fatalf("got %#v", got)
	}

	entry.Options = "defaults"
	content, err = upsertFstabManagedEntry(content, entry)
	if err != nil {
		t.Fatalf("second upsertFstabManagedEntry: %s", err)
	}
	if strings.Count(content, fstabStartMarker("data")) != 1 {
		t.Fatalf("expected one managed block, got content: %q", content)
	}

	content, changed, err := removeFstabManagedEntry(content, "data")
	if err != nil {
		t.Fatalf("removeFstabManagedEntry: %s", err)
	}
	if !changed {
		t.Fatal("expected managed block removal")
	}
	if strings.Contains(content, fstabStartMarker("data")) {
		t.Fatalf("managed block was not removed: %q", content)
	}
}

func TestFstabManagedEntryMissingEndMarker(t *testing.T) {
	t.Parallel()

	content := fstabStartMarker("data") + "\nUUID=1234\t/data\text4\tdefaults\t0\t2\n"
	if _, _, err := readFstabManagedEntry(content, "data"); err == nil {
		t.Fatal("expected missing end marker error")
	}
}

func TestValidateHostFstabEntry(t *testing.T) {
	t.Parallel()

	entry := FstabEntry{
		Name:       "swap",
		Device:     "UUID=1234",
		MountPoint: "none",
		FSType:     "swap",
		Options:    "defaults",
		Dump:       0,
		Pass:       0,
	}
	if err := validateHostFstabEntry(entry); err != nil {
		t.Fatalf("expected swap entry to be valid: %s", err)
	}

	entry.MountPoint = "relative"
	if err := validateHostFstabEntry(entry); err == nil {
		t.Fatal("expected relative mount point to be invalid")
	}
}

func TestHostFstabManagerSerializesConcurrentUpdates(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "fstab")
	if err := os.WriteFile(path, []byte("# existing\n"), 0o644); err != nil {
		t.Fatalf("write fstab fixture: %s", err)
	}

	var activeWriters atomic.Int32
	var maxActiveWriters atomic.Int32
	manager := &HostFstabManager{
		path:     path,
		readFile: readProtectedFile,
		writeFile: func(ctx context.Context, _ string, path string, content []byte, mode os.FileMode) error {
			active := activeWriters.Add(1)
			defer activeWriters.Add(-1)
			for {
				previous := maxActiveWriters.Load()
				if active <= previous || maxActiveWriters.CompareAndSwap(previous, active) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			return writeHostFileAtomically(path, content, mode)
		},
	}

	const entryCount = 12
	start := make(chan struct{})
	errs := make(chan error, entryCount)
	var wg sync.WaitGroup
	for index := range entryCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			name := fmt.Sprintf("data-%02d", index)
			errs <- manager.SyncEntry(t.Context(), FstabEntry{
				Name:       name,
				Device:     "UUID=" + name,
				MountPoint: "/mnt/" + name,
				FSType:     "ext4",
				Options:    "defaults",
				Pass:       2,
			})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("sync fstab entry: %s", err)
		}
	}

	if maxActiveWriters.Load() != 1 {
		t.Fatalf("concurrent fstab writers got %d, want 1", maxActiveWriters.Load())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read final fstab: %s", err)
	}
	for index := range entryCount {
		name := fmt.Sprintf("data-%02d", index)
		if _, exists, err := readFstabManagedEntry(string(content), name); err != nil {
			t.Fatalf("read %s: %s", name, err)
		} else if !exists {
			t.Fatalf("final fstab lost managed entry %q", name)
		}
	}
}
