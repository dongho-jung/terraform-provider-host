package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestValidateCronExpressionAllowsCronOrSemantics(t *testing.T) {
	t.Parallel()

	if err := validateCronExpression("0 3 1 * 1"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestCronExpressionFromEvery(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"1m":  "* * * * *",
		"15m": "*/15 * * * *",
		"1h":  "0 * * * *",
		"6h":  "0 */6 * * *",
		"24h": "0 0 * * *",
	}

	for input, want := range tests {
		got, err := cronExpressionFromEvery(input)
		if err != nil {
			t.Fatalf("cronExpressionFromEvery(%q): %s", input, err)
		}
		if got != want {
			t.Fatalf("cronExpressionFromEvery(%q) got %q, want %q", input, got, want)
		}
	}
}

func TestCronExpressionFromEveryRejectsInexactDuration(t *testing.T) {
	t.Parallel()

	_, err := cronExpressionFromEvery("90m")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cannot be represented exactly") {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestRenderHostScheduleCronEntry(t *testing.T) {
	t.Parallel()

	entry, err := renderHostScheduleCronEntry(
		HostScheduleSpec{
			ID:       "0123456789abcdef",
			Command:  "echo hello",
			Schedule: "*/30 * * * *",
			Shell:    "/bin/sh",
			Enabled:  true,
		},
		HostScheduleStatus{
			ID:         "0123456789abcdef",
			ScriptPath: "/tmp/provider schedules/run.sh",
		},
	)
	if err != nil {
		t.Fatalf("render cron entry: %s", err)
	}

	want := []string{
		"# terraform-provider-host schedule 0123456789abcdef",
		"*/30 * * * * '/tmp/provider schedules/run.sh'",
	}
	for i := range want {
		if entry[i] != want[i] {
			t.Fatalf("entry[%d] got %q, want %q", i, entry[i], want[i])
		}
	}
}

func TestFilterHostScheduleCronEntry(t *testing.T) {
	t.Parallel()

	lines := []string{
		"SHELL=/bin/zsh",
		"# terraform-provider-host schedule 0123456789abcdef",
		"*/30 * * * * /tmp/run.sh",
		"0 9 * * * /usr/bin/true",
	}

	got := filterHostScheduleCronEntry(lines, "0123456789abcdef", "/tmp/run.sh")
	want := []string{
		"SHELL=/bin/zsh",
		"0 9 * * * /usr/bin/true",
	}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestFilterHostScheduleCronEntryRemovesLegacyPathWithoutMarker(t *testing.T) {
	t.Parallel()

	id := "0123456789abcdef"
	lines := []string{
		"0 1 * * * '/old/checkout/.terraform-provider-host/schedules/" + id + "/run.sh'",
		"0 9 * * * /usr/bin/true",
	}

	got := filterHostScheduleCronEntry(lines, id, "/home/terraform/.local/state/terraform-provider-host/schedules/"+id+"/run.sh")
	want := []string{"0 9 * * * /usr/bin/true"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestInspectHostScheduleCronEntryRequiresExactManagedEntry(t *testing.T) {
	t.Parallel()

	spec := HostScheduleSpec{
		ID:       "0123456789abcdef",
		Schedule: "*/30 * * * *",
		Enabled:  true,
	}
	status := HostScheduleStatus{
		ID:         spec.ID,
		ScriptPath: "/tmp/provider schedules/run.sh",
	}
	expected, err := renderHostScheduleCronEntry(spec, status)
	if err != nil {
		t.Fatalf("render cron entry: %s", err)
	}

	tests := []struct {
		name    string
		lines   []string
		present bool
		matches bool
	}{
		{name: "exact", lines: expected, present: true, matches: true},
		{name: "marker only", lines: expected[:1], present: true, matches: false},
		{name: "wrong expression", lines: []string{expected[0], "0 1 * * * '/tmp/provider schedules/run.sh'"}, present: true, matches: false},
		{name: "script only", lines: expected[1:], present: true, matches: false},
		{name: "legacy script only", lines: []string{"0 1 * * * '/old/.terraform-provider-host/schedules/0123456789abcdef/run.sh'"}, present: true, matches: false},
		{name: "duplicate", lines: append(append([]string{}, expected...), expected...), present: true, matches: false},
		{name: "absent", lines: []string{"0 9 * * * /usr/bin/true"}, present: false, matches: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			present, matches, err := inspectHostScheduleCronEntry(tt.lines, spec, status)
			if err != nil {
				t.Fatalf("inspect cron entry: %s", err)
			}
			if present != tt.present || matches != tt.matches {
				t.Fatalf("got present=%t matches=%t, want present=%t matches=%t", present, matches, tt.present, tt.matches)
			}
		})
	}
}

func TestInspectHostScheduleCronEntryDisabledRequiresAbsence(t *testing.T) {
	t.Parallel()

	spec := HostScheduleSpec{
		ID:       "0123456789abcdef",
		Schedule: "0 * * * *",
		Enabled:  false,
	}
	status := HostScheduleStatus{ID: spec.ID, ScriptPath: "/tmp/run.sh"}

	present, matches, err := inspectHostScheduleCronEntry(nil, spec, status)
	if err != nil {
		t.Fatalf("inspect absent cron entry: %s", err)
	}
	if present || !matches {
		t.Fatalf("got present=%t matches=%t, want false/true", present, matches)
	}

	present, matches, err = inspectHostScheduleCronEntry([]string{
		hostScheduleCronMarkerPrefix + spec.ID,
		"0 * * * * '/tmp/run.sh'",
	}, spec, status)
	if err != nil {
		t.Fatalf("inspect present cron entry: %s", err)
	}
	if !present || matches {
		t.Fatalf("got present=%t matches=%t, want true/false", present, matches)
	}
}

func TestRenderHostScheduleScript(t *testing.T) {
	t.Parallel()

	script, err := renderHostScheduleScript(HostScheduleSpec{
		ID:         "0123456789abcdef",
		Command:    "echo '$HELLO'",
		Schedule:   "0 * * * *",
		Shell:      "/bin/zsh",
		StdoutPath: "/tmp/stdout.log",
		StderrPath: "/tmp/stderr.log",
		Environment: map[string]string{
			"HELLO": "hello world",
		},
	})
	if err != nil {
		t.Fatalf("render script: %s", err)
	}

	for _, want := range []string{
		"#!/bin/zsh\n",
		"exec >> '/tmp/stdout.log'\n",
		"exec 2>> '/tmp/stderr.log'\n",
		"export HELLO='hello world'\n",
		"echo '$HELLO'\n",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("expected script to contain %q:\n%s", want, script)
		}
	}
}

func TestInspectHostScheduleRuntimeDetectsAndRepairsDrift(t *testing.T) {
	runtimeRoot := t.TempDir()
	user, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	spec := HostScheduleSpec{
		ID:       "0123456789abcdef",
		User:     user,
		Command:  "echo hello",
		Schedule: "0 * * * *",
		Shell:    "/bin/sh",
		Enabled:  true,
	}
	status, err := hostScheduleStatusForProvider(spec, "", runtimeRoot)
	if err != nil {
		t.Fatalf("schedule status: %s", err)
	}
	writeRuntime := func() {
		t.Helper()
		if err := writeHostScheduleRuntimeFilesForProvider(spec, status, "", runtimeRoot); err != nil {
			t.Fatalf("write schedule runtime: %s", err)
		}
	}
	assertHealth := func(wantValid bool) {
		t.Helper()
		health, err := inspectHostScheduleRuntimeForProvider(spec, status, "", runtimeRoot)
		if err != nil {
			t.Fatalf("inspect schedule runtime: %s", err)
		}
		if !health.Present || health.Valid != wantValid {
			t.Fatalf("got health %#v, want present=true valid=%t", health, wantValid)
		}
	}

	writeRuntime()
	assertHealth(true)

	if err := os.WriteFile(status.ScriptPath, []byte("#!/bin/sh\nfalse\n"), 0o700); err != nil {
		t.Fatalf("corrupt script: %s", err)
	}
	assertHealth(false)
	writeRuntime()
	assertHealth(true)

	metadataPath, err := hostScheduleMetadataPathForRuntime(spec.ID, runtimeRoot)
	if err != nil {
		t.Fatalf("metadata path: %s", err)
	}
	if err := os.WriteFile(metadataPath, []byte("not json\n"), 0o600); err != nil {
		t.Fatalf("corrupt metadata: %s", err)
	}
	assertHealth(false)
	writeRuntime()
	assertHealth(true)

	if err := os.Chmod(status.ScriptPath, 0o600); err != nil {
		t.Fatalf("change script mode: %s", err)
	}
	assertHealth(false)
	writeRuntime()
	assertHealth(true)

	if err := os.Remove(status.ScriptPath); err != nil {
		t.Fatalf("remove script: %s", err)
	}
	assertHealth(false)
	writeRuntime()
	assertHealth(true)
}

func TestCLICronScheduleManagerReadDetectsRuntimeAndCronDrift(t *testing.T) {
	crontabPath, crontabStatePath := newTestCrontabExecutable(t)
	runtimeRoot := t.TempDir()
	user, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	manager := NewCLICronScheduleManager(crontabPath, nil, "", CLICronScheduleManagerOptions{
		RuntimeDir: runtimeRoot,
		TargetUser: user,
	})
	spec := HostScheduleSpec{
		ID:       "0123456789abcdef",
		User:     user,
		Command:  "echo hello",
		Schedule: "0 * * * *",
		Shell:    "/bin/sh",
		Enabled:  true,
	}

	status, err := manager.UpsertSchedule(t.Context(), spec)
	if err != nil {
		t.Fatalf("upsert schedule: %s", err)
	}
	readStatus, exists, err := manager.ReadSchedule(t.Context(), spec)
	if err != nil {
		t.Fatalf("read healthy schedule: %s", err)
	}
	if !exists || readStatus.RuntimeDrifted {
		t.Fatalf("healthy schedule got exists=%t status=%#v", exists, readStatus)
	}

	if err := os.WriteFile(status.ScriptPath, []byte("#!/bin/sh\nfalse\n"), 0o700); err != nil {
		t.Fatalf("corrupt script: %s", err)
	}
	readStatus, exists, err = manager.ReadSchedule(t.Context(), spec)
	if err != nil {
		t.Fatalf("read corrupt schedule: %s", err)
	}
	if !exists || !readStatus.RuntimeDrifted {
		t.Fatalf("corrupt schedule got exists=%t status=%#v", exists, readStatus)
	}

	if _, err := manager.UpsertSchedule(t.Context(), spec); err != nil {
		t.Fatalf("repair schedule: %s", err)
	}
	readStatus, exists, err = manager.ReadSchedule(t.Context(), spec)
	if err != nil {
		t.Fatalf("read repaired schedule: %s", err)
	}
	if !exists || readStatus.RuntimeDrifted {
		t.Fatalf("repaired schedule got exists=%t status=%#v", exists, readStatus)
	}

	if err := os.WriteFile(crontabStatePath, []byte(
		hostScheduleCronMarkerPrefix+spec.ID+"\n0 1 * * * "+shellQuote(status.ScriptPath)+"\n",
	), 0o600); err != nil {
		t.Fatalf("corrupt cron entry: %s", err)
	}
	readStatus, exists, err = manager.ReadSchedule(t.Context(), spec)
	if err != nil {
		t.Fatalf("read cron drift: %s", err)
	}
	if !exists || !readStatus.RuntimeDrifted {
		t.Fatalf("cron drift got exists=%t status=%#v", exists, readStatus)
	}

	if err := os.RemoveAll(status.RuntimeDir); err != nil {
		t.Fatalf("remove runtime: %s", err)
	}
	if err := os.Remove(crontabStatePath); err != nil {
		t.Fatalf("remove cron state: %s", err)
	}
	_, exists, err = manager.ReadSchedule(t.Context(), spec)
	if err != nil {
		t.Fatalf("read absent schedule: %s", err)
	}
	if exists {
		t.Fatal("fully absent schedule unexpectedly exists")
	}
}

func TestCLICronScheduleManagerSerializesParallelCrontabUpdates(t *testing.T) {
	crontabPath, crontabStatePath := newTestCrontabExecutableWithReadDelay(t, "0.05")
	user, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	manager := NewCLICronScheduleManager(crontabPath, nil, "", CLICronScheduleManagerOptions{
		RuntimeDir: t.TempDir(),
		TargetUser: user,
	})

	const scheduleCount = 8
	var waitGroup sync.WaitGroup
	errors := make(chan error, scheduleCount)
	for index := 0; index < scheduleCount; index++ {
		spec := HostScheduleSpec{
			ID:       fmt.Sprintf("%016x", index+1),
			User:     user,
			Command:  "true",
			Schedule: "0 * * * *",
			Shell:    "/bin/sh",
			Enabled:  true,
		}
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, upsertErr := manager.UpsertSchedule(t.Context(), spec)
			errors <- upsertErr
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("parallel upsert: %s", err)
		}
	}

	content, err := os.ReadFile(crontabStatePath)
	if err != nil {
		t.Fatalf("read final crontab: %s", err)
	}
	for index := 0; index < scheduleCount; index++ {
		marker := hostScheduleCronMarkerPrefix + fmt.Sprintf("%016x", index+1)
		if strings.Count(string(content), marker) != 1 {
			t.Fatalf("final crontab does not contain exactly one %q:\n%s", marker, content)
		}
	}
}

func newTestCrontabExecutable(t *testing.T) (string, string) {
	return newTestCrontabExecutableWithReadDelay(t, "")
}

func newTestCrontabExecutableWithReadDelay(t *testing.T, readDelay string) (string, string) {
	t.Helper()

	root := t.TempDir()
	crontabPath := filepath.Join(root, "crontab")
	statePath := filepath.Join(root, "state")
	readDelayCommand := ""
	if readDelay != "" {
		readDelayCommand = "  sleep " + shellQuote(readDelay) + "\n"
	}
	script := "#!/bin/sh\n" +
		"state=" + shellQuote(statePath) + "\n" +
		"if [ \"$1\" = \"-l\" ]; then\n" +
		"  if [ -f \"$state\" ]; then cat \"$state\"; fi\n" +
		readDelayCommand +
		"  exit 0\n" +
		"fi\n" +
		"cp \"$1\" \"$state\"\n"
	if err := os.WriteFile(crontabPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake crontab: %s", err)
	}

	return crontabPath, statePath
}

func TestWriteHostScheduleRuntimeReplacesSymlink(t *testing.T) {
	runtimeRoot := t.TempDir()
	user, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	spec := HostScheduleSpec{
		ID:       "0123456789abcdef",
		User:     user,
		Command:  "true",
		Schedule: "0 * * * *",
		Shell:    "/bin/sh",
		Enabled:  true,
	}
	status, err := hostScheduleStatusForProvider(spec, "", runtimeRoot)
	if err != nil {
		t.Fatalf("schedule status: %s", err)
	}
	if err := os.MkdirAll(status.RuntimeDir, 0o700); err != nil {
		t.Fatalf("create runtime dir: %s", err)
	}
	targetPath := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(targetPath, []byte("keep me\n"), 0o600); err != nil {
		t.Fatalf("write symlink target: %s", err)
	}
	if err := os.Symlink(targetPath, status.ScriptPath); err != nil {
		t.Fatalf("create script symlink: %s", err)
	}

	if err := writeHostScheduleRuntimeFilesForProvider(spec, status, "", runtimeRoot); err != nil {
		t.Fatalf("write schedule runtime: %s", err)
	}
	info, err := os.Lstat(status.ScriptPath)
	if err != nil {
		t.Fatalf("inspect script: %s", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("script mode got %s, want regular file", info.Mode())
	}
	targetContent, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read symlink target: %s", err)
	}
	if string(targetContent) != "keep me\n" {
		t.Fatalf("symlink target was modified: %q", targetContent)
	}
}

func TestWriteHostScheduleRuntimeReplacesRuntimeDirectorySymlink(t *testing.T) {
	runtimeRoot := t.TempDir()
	user, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	spec := HostScheduleSpec{
		ID:       "0123456789abcdef",
		User:     user,
		Command:  "true",
		Schedule: "0 * * * *",
		Shell:    "/bin/sh",
		Enabled:  true,
	}
	status, err := hostScheduleStatusForProvider(spec, "", runtimeRoot)
	if err != nil {
		t.Fatalf("schedule status: %s", err)
	}
	if err := os.MkdirAll(filepath.Dir(status.RuntimeDir), 0o700); err != nil {
		t.Fatalf("create schedules root: %s", err)
	}
	symlinkTarget := t.TempDir()
	sentinelPath := filepath.Join(symlinkTarget, "sentinel")
	if err := os.WriteFile(sentinelPath, []byte("keep me\n"), 0o600); err != nil {
		t.Fatalf("write symlink target sentinel: %s", err)
	}
	if err := os.Symlink(symlinkTarget, status.RuntimeDir); err != nil {
		t.Fatalf("create runtime symlink: %s", err)
	}

	if err := writeHostScheduleRuntimeFilesForProvider(spec, status, "", runtimeRoot); err != nil {
		t.Fatalf("write schedule runtime: %s", err)
	}
	info, err := os.Lstat(status.RuntimeDir)
	if err != nil {
		t.Fatalf("inspect runtime directory: %s", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("runtime mode got %s, want real directory", info.Mode())
	}
	if _, err := os.Stat(sentinelPath); err != nil {
		t.Fatalf("symlink target was modified: %s", err)
	}
}

func TestApplyHostScheduleTargetUserUsesConfiguredUser(t *testing.T) {
	t.Parallel()

	spec := HostScheduleSpec{}
	if err := applyHostScheduleTargetUser(&spec, "deploy"); err != nil {
		t.Fatalf("applyHostScheduleTargetUser: %s", err)
	}
	if spec.User != "deploy" {
		t.Fatalf("user got %q, want deploy", spec.User)
	}
}

func TestApplyHostScheduleTargetUserRequiresConfiguredUser(t *testing.T) {
	t.Parallel()

	err := applyHostScheduleTargetUser(&HostScheduleSpec{}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "target user") {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestHostScheduleCrontabArgsCurrentUser(t *testing.T) {
	t.Parallel()

	got, targetsOtherUser := hostScheduleCrontabArgs("dongho", "dongho", "-l")
	if targetsOtherUser {
		t.Fatal("expected current user target")
	}
	if len(got) != 1 || got[0] != "-l" {
		t.Fatalf("got %#v", got)
	}
}

func TestHostScheduleCrontabArgsOtherUser(t *testing.T) {
	t.Parallel()

	got, targetsOtherUser := hostScheduleCrontabArgs("root", "dongho", "-l")
	if !targetsOtherUser {
		t.Fatal("expected other user target")
	}
	want := []string{"-u", "root", "-l"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}
