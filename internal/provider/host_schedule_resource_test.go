package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type recordingHostScheduleManager struct {
	readSpecs []HostScheduleSpec
	status    HostScheduleStatus
	exists    bool
}

func (m *recordingHostScheduleManager) UpsertSchedule(context.Context, HostScheduleSpec) (HostScheduleStatus, error) {
	return m.status, nil
}

func (m *recordingHostScheduleManager) ReadSchedule(_ context.Context, spec HostScheduleSpec) (HostScheduleStatus, bool, error) {
	m.readSpecs = append(m.readSpecs, spec)
	return m.status, m.exists, nil
}

func (m *recordingHostScheduleManager) DeleteSchedule(context.Context, HostScheduleSpec) error {
	return nil
}

func TestHostScheduleImportStateHydratesRuntimeMetadataBeforeRead(t *testing.T) {
	ctx := t.Context()
	runtimeRoot := t.TempDir()
	homeDir := t.TempDir()
	id := "0123456789abcdef"
	targetUser, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	spec := HostScheduleSpec{
		ID:               id,
		User:             targetUser,
		Command:          "printf 'imported schedule\\n'",
		Schedule:         "15 2 * * *",
		Shell:            "/bin/sh",
		Enabled:          false,
		WorkingDirectory: "~/work",
		Environment:      map[string]string{"IMPORT_TEST": "true"},
		StdoutPath:       "~/logs/schedule.out",
		StderrPath:       "~/logs/schedule.err",
	}
	status, err := hostScheduleStatusForProvider(spec, homeDir, runtimeRoot)
	if err != nil {
		t.Fatalf("schedule status: %s", err)
	}
	if err := writeHostScheduleRuntimeFilesForProvider(spec, status, homeDir, runtimeRoot); err != nil {
		t.Fatalf("write runtime metadata: %s", err)
	}

	manager := &recordingHostScheduleManager{status: status, exists: true}
	r := &HostScheduleResource{
		manager:    manager,
		homeDir:    homeDir,
		runtimeDir: runtimeRoot,
		targetUser: targetUser,
	}
	var schemaResp frameworkresource.SchemaResponse
	r.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}

	importResp := frameworkresource.ImportStateResponse{
		State: tfsdk.State{
			Schema: schemaResp.Schema,
			Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
		},
	}
	r.ImportState(ctx, frameworkresource.ImportStateRequest{ID: id}, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", importResp.Diagnostics)
	}
	var imported HostScheduleResourceModel
	if diags := importResp.State.Get(ctx, &imported); diags.HasError() {
		t.Fatalf("decode imported state: %v", diags)
	}
	if !imported.Command.IsNull() {
		t.Fatalf("import command got %#v, want null before Read", imported.Command)
	}

	readResp := frameworkresource.ReadResponse{State: importResp.State}
	r.Read(ctx, frameworkresource.ReadRequest{State: importResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResp.Diagnostics)
	}
	if len(manager.readSpecs) != 1 || !reflect.DeepEqual(manager.readSpecs[0], spec) {
		t.Fatalf("manager read specs got %#v, want %#v", manager.readSpecs, spec)
	}

	var got HostScheduleResourceModel
	if diags := readResp.State.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode read state: %v", diags)
	}
	if got.Command.ValueString() != spec.Command || got.Schedule.ValueString() != spec.Schedule || !got.Every.IsNull() {
		t.Fatalf("schedule config was not hydrated: command=%#v schedule=%#v every=%#v", got.Command, got.Schedule, got.Every)
	}
	if got.Shell.ValueString() != spec.Shell || got.Enabled.ValueBool() != spec.Enabled {
		t.Fatalf("schedule defaults were not hydrated: shell=%#v enabled=%#v", got.Shell, got.Enabled)
	}
	if got.WorkingDirectory.ValueString() != spec.WorkingDirectory || got.StdoutPath.ValueString() != spec.StdoutPath || got.StderrPath.ValueString() != spec.StderrPath {
		t.Fatalf("schedule paths were not hydrated: working=%#v stdout=%#v stderr=%#v", got.WorkingDirectory, got.StdoutPath, got.StderrPath)
	}
	var environment map[string]string
	if diags := got.Environment.ElementsAs(ctx, &environment, false); diags.HasError() {
		t.Fatalf("decode imported environment: %v", diags)
	}
	if !reflect.DeepEqual(environment, spec.Environment) {
		t.Fatalf("environment got %#v, want %#v", environment, spec.Environment)
	}
	if got.Backend.ValueString() != "cron" || got.RuntimeDir.ValueString() != status.RuntimeDir || got.ScriptPath.ValueString() != status.ScriptPath {
		t.Fatalf("computed state was not refreshed: backend=%#v runtime=%#v script=%#v", got.Backend, got.RuntimeDir, got.ScriptPath)
	}
}

func TestHostScheduleReadDoesNotReplaceConfiguredStateFromMetadata(t *testing.T) {
	ctx := t.Context()
	runtimeRoot := t.TempDir()
	homeDir := t.TempDir()
	id := "0123456789abcdef"
	targetUser, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	metadataSpec := HostScheduleSpec{ID: id, User: targetUser, Command: "metadata command", Every: "1h", Shell: "/bin/sh", Enabled: true}
	status, err := hostScheduleStatusForProvider(metadataSpec, homeDir, runtimeRoot)
	if err != nil {
		t.Fatalf("schedule status: %s", err)
	}
	if err := writeHostScheduleRuntimeFilesForProvider(metadataSpec, status, homeDir, runtimeRoot); err != nil {
		t.Fatalf("write runtime metadata: %s", err)
	}

	configuredSpec := metadataSpec
	configuredSpec.Command = "configured command"
	manager := &recordingHostScheduleManager{status: status, exists: true}
	r := &HostScheduleResource{manager: manager, homeDir: homeDir, runtimeDir: runtimeRoot, targetUser: targetUser}
	var schemaResp frameworkresource.SchemaResponse
	r.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}
	model := HostScheduleResourceModel{}
	if diags := hydrateHostScheduleConfigState(ctx, &model, configuredSpec); diags.HasError() {
		t.Fatalf("hydrate configured state: %v", diags)
	}
	hydrateHostScheduleComputedState(&model, status)
	state := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)}
	if diags := state.Set(ctx, &model); diags.HasError() {
		t.Fatalf("encode configured state: %v", diags)
	}

	readResp := frameworkresource.ReadResponse{State: state}
	r.Read(ctx, frameworkresource.ReadRequest{State: state}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResp.Diagnostics)
	}
	if len(manager.readSpecs) != 1 || manager.readSpecs[0].Command != configuredSpec.Command {
		t.Fatalf("manager command got %#v, want configured command %q", manager.readSpecs, configuredSpec.Command)
	}
}

func TestLoadHostScheduleImportSpecValidatesMetadataIdentity(t *testing.T) {
	targetUser, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	id := "0123456789abcdef"
	baseSpec := HostScheduleSpec{ID: id, User: targetUser, Command: "true", Every: "30m", Shell: "/bin/sh", Enabled: true}

	tests := []struct {
		name   string
		mutate func(*hostScheduleMetadata)
		want   string
	}{
		{
			name: "ID",
			mutate: func(metadata *hostScheduleMetadata) {
				metadata.Spec.ID = "fedcba9876543210"
			},
			want: "has ID",
		},
		{
			name: "backend",
			mutate: func(metadata *hostScheduleMetadata) {
				metadata.Backend = "systemd"
			},
			want: "unsupported backend",
		},
		{
			name: "script path",
			mutate: func(metadata *hostScheduleMetadata) {
				metadata.ScriptPath = "/tmp/untrusted-run.sh"
			},
			want: "has script path",
		},
		{
			name: "target user",
			mutate: func(metadata *hostScheduleMetadata) {
				metadata.Spec.User = "another-user"
			},
			want: "provider targets",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runtimeRoot := t.TempDir()
			metadataPath, err := hostScheduleMetadataPathForRuntime(id, runtimeRoot)
			if err != nil {
				t.Fatalf("metadata path: %s", err)
			}
			if err := os.MkdirAll(filepath.Dir(metadataPath), 0o700); err != nil {
				t.Fatalf("create runtime directory: %s", err)
			}
			metadata := hostScheduleMetadata{Spec: baseSpec, Backend: "cron", ScriptPath: filepath.Join(filepath.Dir(metadataPath), "run.sh")}
			tc.mutate(&metadata)
			metadataBytes, err := json.Marshal(metadata)
			if err != nil {
				t.Fatalf("encode metadata: %s", err)
			}
			if err := os.WriteFile(metadataPath, metadataBytes, 0o600); err != nil {
				t.Fatalf("write metadata: %s", err)
			}

			_, err = loadHostScheduleImportSpec(id, "", runtimeRoot, targetUser)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("metadata validation error got %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestLoadHostScheduleImportSpecRejectsNonRegularMetadata(t *testing.T) {
	targetUser, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	id := "0123456789abcdef"
	runtimeRoot := t.TempDir()
	metadataPath, err := hostScheduleMetadataPathForRuntime(id, runtimeRoot)
	if err != nil {
		t.Fatalf("metadata path: %s", err)
	}
	if err := os.MkdirAll(filepath.Dir(metadataPath), 0o700); err != nil {
		t.Fatalf("create runtime directory: %s", err)
	}
	targetPath := filepath.Join(t.TempDir(), "metadata.json")
	if err := os.WriteFile(targetPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write symlink target: %s", err)
	}
	if err := os.Symlink(targetPath, metadataPath); err != nil {
		t.Fatalf("create metadata symlink: %s", err)
	}

	_, err = loadHostScheduleImportSpec(id, "", runtimeRoot, targetUser)
	if err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("symlink metadata error got %v", err)
	}
}

func TestHostScheduleCleanupAbandonedRuntimeRemovesRuntimeDir(t *testing.T) {
	t.Parallel()

	runtimeRoot := t.TempDir()
	id := "0123456789abcdef"

	user, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	spec := HostScheduleSpec{
		ID:      id,
		User:    user,
		Command: "true",
		Every:   "30m",
		Shell:   "/bin/sh",
		Enabled: true,
	}

	status, err := hostScheduleStatusForProvider(spec, "", runtimeRoot)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if err := writeHostScheduleRuntimeFilesForProvider(spec, status, "", runtimeRoot); err != nil {
		t.Fatalf("write runtime files: %s", err)
	}
	if _, err := os.Stat(status.ScriptPath); err != nil {
		t.Fatalf("expected runtime script to exist: %s", err)
	}

	resource := &HostScheduleResource{runtimeDir: runtimeRoot}
	resource.cleanupAbandonedScheduleRuntime(id)

	if _, err := os.Stat(status.RuntimeDir); !os.IsNotExist(err) {
		t.Fatalf("expected runtime directory to be removed, got stat error %v", err)
	}
}

func TestHydrateHostScheduleReadStateMarksRuntimeDriftForRepair(t *testing.T) {
	t.Parallel()

	model := HostScheduleResourceModel{
		RuntimeDir: types.StringValue("/tmp/runtime/schedules/0123456789abcdef"),
		ScriptPath: types.StringValue("/tmp/runtime/schedules/0123456789abcdef/run.sh"),
	}
	status := HostScheduleStatus{
		ID:             "0123456789abcdef",
		User:           "terraform",
		Backend:        "cron",
		RuntimeDir:     "/tmp/runtime/schedules/0123456789abcdef",
		ScriptPath:     "/tmp/runtime/schedules/0123456789abcdef/run.sh",
		RuntimeDrifted: true,
	}

	hydrateHostScheduleReadState(&model, status)
	if !model.ScriptPath.IsNull() {
		t.Fatalf("script_path got %#v, want null drift marker", model.ScriptPath)
	}

	status.RuntimeDrifted = false
	hydrateHostScheduleComputedState(&model, status)
	if model.ScriptPath.ValueString() != status.ScriptPath {
		t.Fatalf("planned script_path got %q, want %q", model.ScriptPath.ValueString(), status.ScriptPath)
	}
}

func TestHydrateHostScheduleReadStatePreservesPreviousRuntimeForMigration(t *testing.T) {
	t.Parallel()

	id := "0123456789abcdef"
	previousRuntimeDir := "/tmp/checkout/.terraform-provider-host/schedules/" + id
	previousScriptPath := previousRuntimeDir + "/run.sh"
	model := HostScheduleResourceModel{
		RuntimeDir: types.StringValue(previousRuntimeDir),
		ScriptPath: types.StringValue(previousScriptPath),
	}
	status := HostScheduleStatus{
		ID:             id,
		User:           "terraform",
		Backend:        "cron",
		RuntimeDir:     "/home/terraform/.local/state/terraform-provider-host/schedules/" + id,
		ScriptPath:     "/home/terraform/.local/state/terraform-provider-host/schedules/" + id + "/run.sh",
		RuntimeDrifted: true,
	}

	hydrateHostScheduleReadState(&model, status)
	if model.RuntimeDir.ValueString() != previousRuntimeDir || model.ScriptPath.ValueString() != previousScriptPath {
		t.Fatalf("previous runtime was not preserved: runtime=%q script=%q", model.RuntimeDir.ValueString(), model.ScriptPath.ValueString())
	}
}

func TestCleanupPreviousHostScheduleRuntimeRemovesVerifiedLegacyArtifact(t *testing.T) {
	baseDir := t.TempDir()
	legacyRoot := filepath.Join(baseDir, providerLegacyRuntimeDirName)
	currentRoot := filepath.Join(baseDir, ".local", "state", providerRuntimeDirName)
	id := "0123456789abcdef"
	user, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	spec := HostScheduleSpec{
		ID:      id,
		User:    user,
		Command: "true",
		Every:   "30m",
		Shell:   "/bin/sh",
		Enabled: true,
	}
	previousStatus, err := hostScheduleStatusForProvider(spec, "", legacyRoot)
	if err != nil {
		t.Fatalf("previous status: %s", err)
	}
	if err := writeHostScheduleRuntimeFilesForProvider(spec, previousStatus, "", legacyRoot); err != nil {
		t.Fatalf("write previous runtime: %s", err)
	}
	currentStatus, err := hostScheduleStatusForProvider(spec, "", currentRoot)
	if err != nil {
		t.Fatalf("current status: %s", err)
	}
	state := HostScheduleResourceModel{
		RuntimeDir: types.StringValue(previousStatus.RuntimeDir),
		ScriptPath: types.StringValue(previousStatus.ScriptPath),
	}

	if err := cleanupPreviousHostScheduleRuntime(state, currentStatus, legacyRoot); err != nil {
		t.Fatalf("cleanup previous runtime: %s", err)
	}
	if _, err := os.Stat(previousStatus.RuntimeDir); !os.IsNotExist(err) {
		t.Fatalf("previous runtime still exists: %v", err)
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("empty legacy root still exists: %v", err)
	}
}

func TestCleanupPreviousHostScheduleRuntimeKeepsUnverifiedArtifact(t *testing.T) {
	baseDir := t.TempDir()
	id := "0123456789abcdef"
	previousRuntimeDir := filepath.Join(baseDir, providerLegacyRuntimeDirName, hostScheduleRuntimeDirName, id)
	if err := os.MkdirAll(previousRuntimeDir, 0o700); err != nil {
		t.Fatalf("create previous runtime: %s", err)
	}
	if err := os.WriteFile(filepath.Join(previousRuntimeDir, "metadata.json"), []byte("corrupt\n"), 0o600); err != nil {
		t.Fatalf("write corrupt metadata: %s", err)
	}
	state := HostScheduleResourceModel{RuntimeDir: types.StringValue(previousRuntimeDir)}
	status := HostScheduleStatus{
		ID:         id,
		RuntimeDir: filepath.Join(baseDir, ".local", "state", providerRuntimeDirName, hostScheduleRuntimeDirName, id),
	}

	if err := cleanupPreviousHostScheduleRuntime(state, status, filepath.Join(baseDir, providerLegacyRuntimeDirName)); err != nil {
		t.Fatalf("cleanup previous runtime: %s", err)
	}
	if _, err := os.Stat(previousRuntimeDir); err != nil {
		t.Fatalf("unverified runtime should remain: %s", err)
	}
}

func TestCleanupPreviousHostScheduleRuntimeRejectsVerifiedStatePathOutsideAllowedRoot(t *testing.T) {
	baseDir := t.TempDir()
	stateRoot := filepath.Join(baseDir, "state-controlled-root")
	allowedRoot := filepath.Join(baseDir, providerLegacyRuntimeDirName)
	currentRoot := filepath.Join(baseDir, ".local", "state", providerRuntimeDirName)
	id := "0123456789abcdef"
	user, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	spec := HostScheduleSpec{ID: id, User: user, Command: "true", Every: "30m", Shell: "/bin/sh", Enabled: true}
	previousStatus, err := hostScheduleStatusForProvider(spec, "", stateRoot)
	if err != nil {
		t.Fatalf("previous status: %s", err)
	}
	if err := writeHostScheduleRuntimeFilesForProvider(spec, previousStatus, "", stateRoot); err != nil {
		t.Fatalf("write previous runtime: %s", err)
	}
	currentStatus, err := hostScheduleStatusForProvider(spec, "", currentRoot)
	if err != nil {
		t.Fatalf("current status: %s", err)
	}
	state := HostScheduleResourceModel{RuntimeDir: types.StringValue(previousStatus.RuntimeDir)}

	if err := cleanupPreviousHostScheduleRuntime(state, currentStatus, allowedRoot); err != nil {
		t.Fatalf("cleanup outside allowlist: %s", err)
	}
	if _, err := os.Stat(previousStatus.RuntimeDir); err != nil {
		t.Fatalf("verified state-controlled runtime should remain: %s", err)
	}
}

func TestVerifiedPreviousRuntimeKeepsDisabledScheduleForRuntimeMigration(t *testing.T) {
	baseDir := t.TempDir()
	previousRoot := filepath.Join(baseDir, providerLegacyRuntimeDirName)
	currentRoot := filepath.Join(baseDir, ".local", "state", providerRuntimeDirName)
	id := "0123456789abcdef"
	user, err := currentHostUsername()
	if err != nil {
		t.Fatalf("resolve current user: %s", err)
	}
	spec := HostScheduleSpec{ID: id, User: user, Command: "true", Every: "30m", Shell: "/bin/sh", Enabled: false}
	previousStatus, err := hostScheduleStatusForProvider(spec, "", previousRoot)
	if err != nil {
		t.Fatalf("previous status: %s", err)
	}
	if err := writeHostScheduleRuntimeFilesForProvider(spec, previousStatus, "", previousRoot); err != nil {
		t.Fatalf("write previous runtime: %s", err)
	}
	currentStatus, err := hostScheduleStatusForProvider(spec, "", currentRoot)
	if err != nil {
		t.Fatalf("current status: %s", err)
	}
	state := HostScheduleResourceModel{
		RuntimeDir: types.StringValue(previousStatus.RuntimeDir),
		ScriptPath: types.StringValue(previousStatus.ScriptPath),
	}
	verified, err := hasVerifiedPreviousHostScheduleRuntime(state, currentStatus)
	if err != nil || !verified {
		t.Fatalf("verified previous disabled runtime got verified=%t err=%v", verified, err)
	}
	currentStatus.RuntimeDrifted = true
	hydrateHostScheduleReadState(&state, currentStatus)
	if state.RuntimeDir.ValueString() != previousStatus.RuntimeDir || state.ScriptPath.ValueString() != previousStatus.ScriptPath {
		t.Fatalf("disabled migration state was not preserved: runtime=%q script=%q", state.RuntimeDir.ValueString(), state.ScriptPath.ValueString())
	}
}
