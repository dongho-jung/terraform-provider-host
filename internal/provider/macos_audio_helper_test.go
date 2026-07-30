//go:build !windows

package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIMacOSAudioManagerCompilesHelperOnceAndCachesDeviceList(t *testing.T) {
	t.Parallel()

	compilerPath := writeMockSwiftCompiler(t)
	runtimeDir := t.TempDir()
	manager, ok := NewCLIMacOSAudioManager(compilerPath, runtimeDir).(*CLIMacOSAudioManager)
	if !ok {
		t.Fatal("new macOS audio manager returned an unexpected implementation")
	}

	for range 2 {
		devices, err := manager.ListDevices(t.Context())
		if err != nil {
			t.Fatalf("list devices: %s", err)
		}
		if len(devices) != 1 || devices[0].UID != "device-1" {
			t.Fatalf("unexpected devices: %#v", devices)
		}
	}

	secondManager, ok := NewCLIMacOSAudioManager(compilerPath, runtimeDir).(*CLIMacOSAudioManager)
	if !ok {
		t.Fatal("second macOS audio manager returned an unexpected implementation")
	}
	if _, err := secondManager.ListDevices(t.Context()); err != nil {
		t.Fatalf("list devices with second manager: %s", err)
	}

	compileLog, err := os.ReadFile(compilerPath + ".log")
	if err != nil {
		t.Fatalf("read compiler log: %s", err)
	}
	if got := len(splitNonEmptyLines(string(compileLog))); got != 1 {
		t.Fatalf("helper compilations got %d, want 1", got)
	}

	helperMatches, err := filepath.Glob(filepath.Join(runtimeDir, "mac_audio", "helper-*"))
	if err != nil {
		t.Fatalf("glob helper files: %s", err)
	}
	var helperPath string
	for _, match := range helperMatches {
		if !strings.HasSuffix(match, ".swift") {
			helperPath = match
			break
		}
	}
	if helperPath == "" {
		t.Fatal("compiled helper was not installed")
	}
	runLog, err := os.ReadFile(helperPath + ".log")
	if err != nil {
		t.Fatalf("read helper run log: %s", err)
	}
	if got := len(splitNonEmptyLines(string(runLog))); got != 2 {
		t.Fatalf("helper runs got %d, want 2 (one per manager cache)", got)
	}
}

func writeMockSwiftCompiler(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "swiftc")
	contents := `#!/bin/sh
printf '%s\n' "$*" >> "${0}.log"
output=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    output="$1"
  fi
  shift
done
printf '%s\n' \
  '#!/bin/sh' \
  'printf "%s\n" "$*" >> "${0}.log"' \
  'printf "%s\n" "[{\"uid\":\"device-1\",\"name\":\"Output\",\"manufacturer\":\"Test\",\"input_channels\":0,\"output_channels\":2}]"' \
  > "$output"
chmod 700 "$output"
`
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write mock Swift compiler: %s", err)
	}
	return path
}
