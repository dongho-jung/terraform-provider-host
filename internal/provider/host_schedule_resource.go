package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &HostScheduleResource{}
	_ resource.ResourceWithConfigure   = &HostScheduleResource{}
	_ resource.ResourceWithImportState = &HostScheduleResource{}
	_ resource.ResourceWithModifyPlan  = &HostScheduleResource{}
)

type HostScheduleResource struct {
	manager    ScheduleManager
	homeDir    string
	runtimeDir string
	targetUser string
}

type HostScheduleResourceModel struct {
	ID                       types.String `tfsdk:"id"`
	Command                  types.String `tfsdk:"command"`
	Schedule                 types.String `tfsdk:"schedule"`
	Every                    types.String `tfsdk:"every"`
	Shell                    types.String `tfsdk:"shell"`
	Enabled                  types.Bool   `tfsdk:"enabled"`
	WorkingDirectory         types.String `tfsdk:"working_directory"`
	Environment              types.Map    `tfsdk:"environment"`
	StdoutPath               types.String `tfsdk:"stdout_path"`
	StderrPath               types.String `tfsdk:"stderr_path"`
	Backend                  types.String `tfsdk:"backend"`
	RuntimeDir               types.String `tfsdk:"runtime_dir"`
	ScriptPath               types.String `tfsdk:"script_path"`
	WorkingDirectoryResolved types.String `tfsdk:"working_directory_resolved"`
	StdoutPathResolved       types.String `tfsdk:"stdout_path_resolved"`
	StderrPathResolved       types.String `tfsdk:"stderr_path_resolved"`
}

func NewHostScheduleResource() resource.Resource {
	return &HostScheduleResource{}
}

func (r *HostScheduleResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_schedule"
}

func (r *HostScheduleResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a schedule through the provider target user's crontab.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Provider-generated schedule identifier. This is stored in Terraform state and is not configured manually.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"command": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Shell command to run when the schedule fires. The provider writes this command into the schedule's generated `run.sh` runtime file.",
			},
			"schedule": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Five-field cron-style calendar schedule, or one of `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`. Mutually exclusive with `every`.",
			},
			"every": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Interval duration such as `15m`, `1h`, or `24h`. Mutually exclusive with `schedule`.",
			},
			"shell": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("/bin/sh"),
				MarkdownDescription: "Absolute path to the shell used as the generated script interpreter.",
			},
			"enabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Whether the schedule should be present in the provider target user's crontab.",
			},
			"working_directory": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Working directory for the scheduled command. `~` is expanded to the provider `home_dir`.",
			},
			"working_directory_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute working directory path, when `working_directory` is set.",
			},
			"environment": schema.MapAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				MarkdownDescription: "Environment variables passed to the scheduled command.",
			},
			"stdout_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path where the generated script appends stdout for the command. `~` is expanded to the provider `home_dir`.",
			},
			"stdout_path_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute stdout log path, when `stdout_path` is set.",
			},
			"stderr_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path where the generated script appends stderr for the command. `~` is expanded to the provider `home_dir`.",
			},
			"stderr_path_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute stderr log path, when `stderr_path` is set.",
			},
			"backend": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Scheduler backend currently managing this schedule. This provider currently writes cron schedules.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"runtime_dir": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Generated schedule runtime directory. By default this is `~/.local/state/terraform-provider-host/schedules/<id>` for the provider target user.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"script_path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Generated `run.sh` path inside the schedule runtime directory.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *HostScheduleResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.ScheduleManager == nil {
			resp.Diagnostics.AddError(
				"Schedule backend unavailable",
				hostScheduleBackendUnavailableMessage(),
			)
			return
		}
		r.manager = data.ScheduleManager
		r.homeDir = data.HomeDir
		r.runtimeDir = data.RuntimeDir
		r.targetUser = data.TargetUser
	case ScheduleManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or ScheduleManager, got %T.", req.ProviderData),
		)
	}
}

func (r *HostScheduleResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostScheduleResourceModel
	var state HostScheduleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	if !state.ID.IsNull() && !state.ID.IsUnknown() {
		plan.ID = state.ID
		status, err := hostScheduleStatusForProvider(HostScheduleSpec{
			ID:   state.ID.ValueString(),
			User: r.targetUser,
		}, r.homeDir, r.runtimeDir)
		if err != nil {
			resp.Diagnostics.AddError("Invalid schedule state", err.Error())
			return
		}
		hydrateHostScheduleComputedState(&plan, status)
	} else {
		plan.ID = types.StringUnknown()
		plan.Backend = types.StringUnknown()
		plan.RuntimeDir = types.StringUnknown()
		plan.ScriptPath = types.StringUnknown()
	}

	if scheduleResourceConfigReady(plan) {
		spec, diags := hostScheduleSpecFromModelForTarget(ctx, plan, r.targetUser)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		if spec.ID == "" && !state.ID.IsNull() && !state.ID.IsUnknown() {
			spec.ID = state.ID.ValueString()
		}
		if spec.ID != "" {
			if err := validateHostScheduleSpecForHome(spec, r.homeDir); err != nil {
				resp.Diagnostics.AddError("Invalid schedule", err.Error())
				return
			}
			status, err := hostScheduleStatusForProvider(spec, r.homeDir, r.runtimeDir)
			if err != nil {
				resp.Diagnostics.AddError("Invalid schedule", err.Error())
				return
			}
			hydrateHostScheduleComputedState(&plan, status)
		} else if err := validateHostScheduleConfigForHome(spec, r.homeDir); err != nil {
			resp.Diagnostics.AddError("Invalid schedule", err.Error())
			return
		} else if err := hydrateHostSchedulePathComputedStateForHome(&plan, spec, r.homeDir); err != nil {
			resp.Diagnostics.AddError("Invalid schedule", err.Error())
			return
		}
	}

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostScheduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostScheduleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.requireManager(&resp.Diagnostics) {
		return
	}

	id, err := newHostScheduleID()
	if err != nil {
		resp.Diagnostics.AddError("Failed to create schedule ID", err.Error())
		return
	}
	plan.ID = types.StringValue(id)

	spec, diags := hostScheduleSpecFromModelForTarget(ctx, plan, r.targetUser)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := r.manager.UpsertSchedule(ctx, spec)
	if err != nil {
		r.cleanupAbandonedScheduleRuntime(id)
		resp.Diagnostics.AddError("Failed to sync schedule", err.Error())
		return
	}

	hydrateHostScheduleComputedState(&plan, status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// cleanupAbandonedScheduleRuntime removes runtime files written for a schedule
// whose creation failed before it was recorded in state. Nothing else can
// reference the freshly generated ID, so leftovers would only accumulate as
// orphaned schedules/<id> directories.
func (r *HostScheduleResource) cleanupAbandonedScheduleRuntime(id string) {
	runtimeDir, err := hostScheduleRuntimeDirForRuntime(id, r.runtimeDir)
	if err != nil {
		return
	}
	_ = os.RemoveAll(runtimeDir)
}

func (r *HostScheduleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostScheduleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.requireManager(&resp.Diagnostics) {
		return
	}

	if state.ID.IsNull() || state.ID.IsUnknown() {
		resp.State.RemoveResource(ctx)
		return
	}
	if hostScheduleImportStateNeedsHydration(state) {
		spec, err := loadHostScheduleImportSpec(state.ID.ValueString(), r.homeDir, r.runtimeDir, r.targetUser)
		if err != nil {
			resp.Diagnostics.AddError("Failed to restore imported schedule state", err.Error())
			return
		}
		resp.Diagnostics.Append(hydrateHostScheduleConfigState(ctx, &state, spec)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	spec, diags := hostScheduleSpecFromModelForTarget(ctx, state, r.targetUser)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, exists, err := r.manager.ReadSchedule(ctx, spec)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read schedule", err.Error())
		return
	}
	if !exists {
		verified, verifyErr := hasVerifiedPreviousHostScheduleRuntime(state, status)
		if verifyErr != nil {
			resp.Diagnostics.AddError("Failed to inspect previous schedule runtime", verifyErr.Error())
			return
		}
		if verified {
			// A disabled schedule has no cron entry. During a runtime_dir change,
			// its verified previous runtime is the only evidence that the resource
			// still exists and needs an in-place migration.
			status.RuntimeDrifted = true
			exists = true
		}
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	hydrateHostScheduleReadState(&state, status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostScheduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostScheduleResourceModel
	var state HostScheduleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.requireManager(&resp.Diagnostics) {
		return
	}

	if plan.ID.IsNull() || plan.ID.IsUnknown() {
		plan.ID = state.ID
	}

	spec, diags := hostScheduleSpecFromModelForTarget(ctx, plan, r.targetUser)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	stateSpec, stateDiags := hostScheduleSpecFromModelForTarget(ctx, state, r.targetUser)
	resp.Diagnostics.Append(stateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if stateSpec.ID != "" && stateSpec.User != spec.User {
		if err := r.manager.DeleteSchedule(ctx, stateSpec); err != nil {
			resp.Diagnostics.AddError("Failed to delete previous schedule", err.Error())
			return
		}
	}

	status, err := r.manager.UpsertSchedule(ctx, spec)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync schedule", err.Error())
		return
	}
	if err := cleanupPreviousHostScheduleRuntimeForResource(state, status); err != nil {
		resp.Diagnostics.AddWarning("Failed to clean previous schedule runtime", err.Error())
	}

	hydrateHostScheduleComputedState(&plan, status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *HostScheduleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostScheduleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.requireManager(&resp.Diagnostics) {
		return
	}

	if state.ID.IsNull() || state.ID.IsUnknown() {
		return
	}

	spec, diags := hostScheduleSpecFromModelForTarget(ctx, state, r.targetUser)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.DeleteSchedule(ctx, spec); err != nil {
		resp.Diagnostics.AddError("Failed to delete schedule", err.Error())
		return
	}

	status, err := hostScheduleStatusForProvider(spec, r.homeDir, r.runtimeDir)
	if err == nil {
		if err := cleanupPreviousHostScheduleRuntimeForResource(state, status); err != nil {
			resp.Diagnostics.AddWarning("Failed to clean previous schedule runtime", err.Error())
		}
	}
}

func (r *HostScheduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if err := validateHostScheduleID(req.ID); err != nil {
		resp.Diagnostics.AddError("Invalid schedule import ID", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(req.ID))...)
}

func (r *HostScheduleResource) requireManager(diags *diag.Diagnostics) bool {
	if r.manager != nil {
		return true
	}

	diags.AddError(
		"Schedule backend unavailable",
		hostScheduleBackendUnavailableMessage(),
	)

	return false
}

func hydrateHostScheduleComputedState(model *HostScheduleResourceModel, status HostScheduleStatus) {
	model.ID = types.StringValue(status.ID)
	model.Backend = types.StringValue(status.Backend)
	model.RuntimeDir = types.StringValue(status.RuntimeDir)
	model.ScriptPath = types.StringValue(status.ScriptPath)
	model.WorkingDirectoryResolved = optionalStringStateValue(status.WorkingDirectoryResolved)
	model.StdoutPathResolved = optionalStringStateValue(status.StdoutPathResolved)
	model.StderrPathResolved = optionalStringStateValue(status.StderrPathResolved)
}

func hydrateHostScheduleReadState(model *HostScheduleResourceModel, status HostScheduleStatus) {
	previousRuntimeDir := model.RuntimeDir
	previousScriptPath := model.ScriptPath
	hydrateHostScheduleComputedState(model, status)
	if !status.RuntimeDrifted {
		return
	}

	// Preserve a previous provider-managed runtime path long enough for Update
	// to migrate and remove it. ModifyPlan computes the new stable path, which
	// gives Terraform a concrete in-place repair diff.
	if !previousRuntimeDir.IsNull() && !previousRuntimeDir.IsUnknown() &&
		filepath.Clean(previousRuntimeDir.ValueString()) != filepath.Clean(status.RuntimeDir) &&
		isHostScheduleRuntimeDirForID(previousRuntimeDir.ValueString(), status.ID) {
		model.RuntimeDir = previousRuntimeDir
		if !previousScriptPath.IsUnknown() {
			model.ScriptPath = previousScriptPath
		}
		return
	}

	// script_path is an existing computed attribute, so clearing it records
	// runtime or cron drift without expanding the public resource schema. The
	// next plan restores the expected path and Update rewrites all artifacts.
	model.ScriptPath = types.StringNull()
}

func cleanupPreviousHostScheduleRuntimeForResource(state HostScheduleResourceModel, status HostScheduleStatus) error {
	legacyRoot, err := filepath.Abs(providerLegacyRuntimeDirName)
	if err != nil {
		return fmt.Errorf("resolve legacy schedule runtime root: %w", err)
	}
	return cleanupPreviousHostScheduleRuntime(state, status, legacyRoot)
}

func cleanupPreviousHostScheduleRuntime(state HostScheduleResourceModel, status HostScheduleStatus, allowedPreviousRoots ...string) error {
	previousRuntimeDir, verified, err := verifiedPreviousHostScheduleRuntime(state, status)
	if err != nil || !verified {
		return err
	}
	previousRoot := filepath.Clean(filepath.Dir(filepath.Dir(previousRuntimeDir)))
	allowed := false
	for _, root := range allowedPreviousRoots {
		if root != "" && filepath.IsAbs(root) && filepath.Clean(root) == previousRoot {
			allowed = true
			break
		}
	}
	if !allowed {
		// A state-provided path is not sufficient authority for recursive
		// deletion. Unknown explicit runtime_dir transitions are left behind for
		// manual inspection; only roots independently derived by the caller are
		// eligible for automatic cleanup.
		return nil
	}

	if err := os.RemoveAll(previousRuntimeDir); err != nil {
		return fmt.Errorf("remove previous schedule runtime directory %q: %w", previousRuntimeDir, err)
	}
	cleanupEmptyLegacyScheduleRuntimeParents(previousRuntimeDir)
	return nil
}

func hasVerifiedPreviousHostScheduleRuntime(state HostScheduleResourceModel, status HostScheduleStatus) (bool, error) {
	_, verified, err := verifiedPreviousHostScheduleRuntime(state, status)
	return verified, err
}

func verifiedPreviousHostScheduleRuntime(state HostScheduleResourceModel, status HostScheduleStatus) (string, bool, error) {
	if state.RuntimeDir.IsNull() || state.RuntimeDir.IsUnknown() {
		return "", false, nil
	}

	previousRuntimeDir := filepath.Clean(state.RuntimeDir.ValueString())
	currentRuntimeDir := filepath.Clean(status.RuntimeDir)
	if previousRuntimeDir == currentRuntimeDir || !isHostScheduleRuntimeDirForID(previousRuntimeDir, status.ID) {
		return "", false, nil
	}
	runtimeInfo, err := os.Lstat(previousRuntimeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect previous schedule runtime: %w", err)
	}
	if runtimeInfo.Mode()&os.ModeSymlink != 0 || !runtimeInfo.IsDir() {
		return "", false, nil
	}

	metadataPath := filepath.Join(previousRuntimeDir, "metadata.json")
	metadataInfo, err := os.Lstat(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect previous schedule metadata: %w", err)
	}
	if metadataInfo.Mode()&os.ModeSymlink != 0 || !metadataInfo.Mode().IsRegular() {
		return "", false, nil
	}
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		return "", false, fmt.Errorf("read previous schedule metadata: %w", err)
	}
	var metadata hostScheduleMetadata
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		// A corrupt file is not enough evidence that a state-provided path is
		// safe to remove recursively. Leave it for manual inspection.
		return "", false, nil
	}
	expectedScriptPath := filepath.Join(previousRuntimeDir, "run.sh")
	if metadata.Spec.ID != status.ID || metadata.Backend != "cron" || filepath.Clean(metadata.ScriptPath) != expectedScriptPath {
		return "", false, nil
	}
	return previousRuntimeDir, true, nil
}

func isHostScheduleRuntimeDirForID(runtimeDir string, id string) bool {
	if runtimeDir == "" || !filepath.IsAbs(runtimeDir) || validateHostScheduleID(id) != nil {
		return false
	}
	cleaned := filepath.Clean(runtimeDir)
	return filepath.Base(cleaned) == id && filepath.Base(filepath.Dir(cleaned)) == hostScheduleRuntimeDirName
}

func cleanupEmptyLegacyScheduleRuntimeParents(scheduleRuntimeDir string) {
	schedulesRoot := filepath.Dir(scheduleRuntimeDir)
	runtimeRoot := filepath.Dir(schedulesRoot)
	if filepath.Base(runtimeRoot) != providerLegacyRuntimeDirName {
		return
	}

	// os.Remove only succeeds for empty directories, so unrelated legacy
	// artifacts are never removed.
	_ = os.Remove(schedulesRoot)
	_ = os.Remove(runtimeRoot)
}

func hydrateHostSchedulePathComputedStateForHome(model *HostScheduleResourceModel, spec HostScheduleSpec, homeDir string) error {
	workingDirectoryResolved, err := resolveOptionalHostSchedulePathForHome(spec.WorkingDirectory, homeDir)
	if err != nil {
		return fmt.Errorf("invalid working_directory: %w", err)
	}
	stdoutPathResolved, err := resolveOptionalHostSchedulePathForHome(spec.StdoutPath, homeDir)
	if err != nil {
		return fmt.Errorf("invalid stdout_path: %w", err)
	}
	stderrPathResolved, err := resolveOptionalHostSchedulePathForHome(spec.StderrPath, homeDir)
	if err != nil {
		return fmt.Errorf("invalid stderr_path: %w", err)
	}

	model.WorkingDirectoryResolved = optionalStringStateValue(workingDirectoryResolved)
	model.StdoutPathResolved = optionalStringStateValue(stdoutPathResolved)
	model.StderrPathResolved = optionalStringStateValue(stderrPathResolved)
	return nil
}

func optionalStringStateValue(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

func hostScheduleImportStateNeedsHydration(model HostScheduleResourceModel) bool {
	// ImportState intentionally starts with only the stable schedule ID. A
	// configured resource always has the required command in state, so use its
	// absence as the narrow compatibility signal and never replace normal
	// configuration-backed state from runtime metadata.
	return model.Command.IsNull()
}

func loadHostScheduleImportSpec(id string, homeDir string, runtimeDir string, targetUser string) (HostScheduleSpec, error) {
	if err := validateHostScheduleID(id); err != nil {
		return HostScheduleSpec{}, err
	}
	if err := validateHostScheduleTargetUser(targetUser); err != nil {
		return HostScheduleSpec{}, fmt.Errorf("invalid provider target user: %w", err)
	}

	metadataPath, err := hostScheduleMetadataPathForRuntime(id, runtimeDir)
	if err != nil {
		return HostScheduleSpec{}, err
	}
	metadataBytes, err := readRegularHostScheduleMetadata(metadataPath)
	if err != nil {
		return HostScheduleSpec{}, err
	}

	var metadata hostScheduleMetadata
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		return HostScheduleSpec{}, fmt.Errorf("decode schedule metadata %q: %w", metadataPath, err)
	}
	if metadata.Spec.ID != id {
		return HostScheduleSpec{}, fmt.Errorf("schedule metadata %q has ID %q, expected %q", metadataPath, metadata.Spec.ID, id)
	}
	if metadata.Backend != "cron" {
		return HostScheduleSpec{}, fmt.Errorf("schedule metadata %q has unsupported backend %q", metadataPath, metadata.Backend)
	}
	if metadata.Spec.User != targetUser {
		return HostScheduleSpec{}, fmt.Errorf("schedule metadata %q targets user %q, but the provider targets %q", metadataPath, metadata.Spec.User, targetUser)
	}
	if err := validateHostScheduleSpecForHome(metadata.Spec, homeDir); err != nil {
		return HostScheduleSpec{}, fmt.Errorf("invalid schedule metadata %q: %w", metadataPath, err)
	}

	expectedScriptPath, err := hostScheduleScriptPathForRuntime(id, runtimeDir)
	if err != nil {
		return HostScheduleSpec{}, err
	}
	if metadata.ScriptPath != expectedScriptPath {
		return HostScheduleSpec{}, fmt.Errorf("schedule metadata %q has script path %q, expected %q", metadataPath, metadata.ScriptPath, expectedScriptPath)
	}

	return metadata.Spec, nil
}

func readRegularHostScheduleMetadata(metadataPath string) ([]byte, error) {
	const maximumMetadataSize = 1 << 20

	info, err := os.Lstat(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("inspect schedule metadata %q: %w", metadataPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("schedule metadata %q must be a regular non-symlink file", metadataPath)
	}
	if info.Size() > maximumMetadataSize {
		return nil, fmt.Errorf("schedule metadata %q exceeds %d bytes", metadataPath, maximumMetadataSize)
	}

	file, err := os.Open(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("open schedule metadata %q: %w", metadataPath, err)
	}
	defer file.Close()

	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened schedule metadata %q: %w", metadataPath, err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("schedule metadata %q changed while it was being opened", metadataPath)
	}
	pathInfo, err := os.Lstat(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("reinspect schedule metadata %q: %w", metadataPath, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !os.SameFile(openedInfo, pathInfo) {
		return nil, fmt.Errorf("schedule metadata %q changed while it was being opened", metadataPath)
	}

	metadataBytes, err := io.ReadAll(io.LimitReader(file, maximumMetadataSize+1))
	if err != nil {
		return nil, fmt.Errorf("read schedule metadata %q: %w", metadataPath, err)
	}
	if len(metadataBytes) > maximumMetadataSize {
		return nil, fmt.Errorf("schedule metadata %q exceeds %d bytes", metadataPath, maximumMetadataSize)
	}
	return metadataBytes, nil
}

func hydrateHostScheduleConfigState(ctx context.Context, model *HostScheduleResourceModel, spec HostScheduleSpec) diag.Diagnostics {
	var diags diag.Diagnostics

	model.ID = types.StringValue(spec.ID)
	model.Command = types.StringValue(spec.Command)
	model.Schedule = optionalStringStateValue(spec.Schedule)
	model.Every = optionalStringStateValue(spec.Every)
	model.Shell = types.StringValue(spec.Shell)
	model.Enabled = types.BoolValue(spec.Enabled)
	model.WorkingDirectory = optionalStringStateValue(spec.WorkingDirectory)
	model.StdoutPath = optionalStringStateValue(spec.StdoutPath)
	model.StderrPath = optionalStringStateValue(spec.StderrPath)
	if spec.Environment == nil {
		model.Environment = types.MapNull(types.StringType)
	} else {
		var environmentDiags diag.Diagnostics
		model.Environment, environmentDiags = types.MapValueFrom(ctx, types.StringType, spec.Environment)
		diags.Append(environmentDiags...)
	}

	return diags
}

func hostScheduleSpecFromModelForTarget(ctx context.Context, model HostScheduleResourceModel, defaultUser string) (HostScheduleSpec, diag.Diagnostics) {
	var diags diag.Diagnostics

	environment, envDiags := stringMapValue(ctx, model.Environment, "schedule environment")
	diags.Append(envDiags...)
	if diags.HasError() {
		return HostScheduleSpec{}, diags
	}

	spec := HostScheduleSpec{
		Environment: environment,
	}
	if !model.ID.IsNull() && !model.ID.IsUnknown() {
		spec.ID = model.ID.ValueString()
	}
	spec.User = defaultUser
	if !model.Command.IsNull() && !model.Command.IsUnknown() {
		spec.Command = model.Command.ValueString()
	}
	if !model.Schedule.IsNull() && !model.Schedule.IsUnknown() {
		spec.Schedule = model.Schedule.ValueString()
	}
	if !model.Every.IsNull() && !model.Every.IsUnknown() {
		spec.Every = model.Every.ValueString()
	}
	if !model.Shell.IsNull() && !model.Shell.IsUnknown() {
		spec.Shell = model.Shell.ValueString()
	}
	if !model.Enabled.IsNull() && !model.Enabled.IsUnknown() {
		spec.Enabled = model.Enabled.ValueBool()
	}
	if !model.WorkingDirectory.IsNull() && !model.WorkingDirectory.IsUnknown() {
		spec.WorkingDirectory = model.WorkingDirectory.ValueString()
	}
	if !model.StdoutPath.IsNull() && !model.StdoutPath.IsUnknown() {
		spec.StdoutPath = model.StdoutPath.ValueString()
	}
	if !model.StderrPath.IsNull() && !model.StderrPath.IsUnknown() {
		spec.StderrPath = model.StderrPath.ValueString()
	}
	if err := applyHostScheduleTargetUser(&spec, defaultUser); err != nil {
		diags.AddError("Invalid schedule target", err.Error())
		return HostScheduleSpec{}, diags
	}

	return spec, diags
}

func validateHostScheduleConfigForHome(spec HostScheduleSpec, homeDir string) error {
	spec.ID = "0000000000000000"
	return validateHostScheduleSpecForHome(spec, homeDir)
}

func scheduleResourceConfigReady(model HostScheduleResourceModel) bool {
	if model.Command.IsNull() || model.Command.IsUnknown() {
		return false
	}
	if model.Shell.IsNull() || model.Shell.IsUnknown() ||
		model.Enabled.IsNull() || model.Enabled.IsUnknown() {
		return false
	}
	if model.Schedule.IsUnknown() || model.Every.IsUnknown() ||
		model.WorkingDirectory.IsUnknown() || model.StdoutPath.IsUnknown() || model.StderrPath.IsUnknown() ||
		model.Environment.IsUnknown() {
		return false
	}

	return true
}

func stringMapValue(ctx context.Context, value types.Map, label string) (map[string]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if value.IsNull() {
		return nil, diags
	}
	if value.IsUnknown() {
		diags.AddError("Invalid "+label, label+" map is unknown")
		return nil, diags
	}

	var elements map[string]string
	diags.Append(value.ElementsAs(ctx, &elements, false)...)
	if diags.HasError() {
		return nil, diags
	}

	return elements, diags
}

func hostScheduleBackendUnavailableMessage() string {
	return "`host_schedule` requires `crontab` in PATH. On Fedora-like systems, the provider will try to install `cronie` when DNF is available."
}
