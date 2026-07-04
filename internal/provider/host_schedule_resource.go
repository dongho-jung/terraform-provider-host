package provider

import (
	"context"
	"fmt"

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
}

type HostScheduleResourceModel struct {
	ID                       types.String `tfsdk:"id"`
	User                     types.String `tfsdk:"user"`
	Scope                    types.String `tfsdk:"scope"`
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
		MarkdownDescription: "Manages a user schedule through the user's crontab.",
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
				MarkdownDescription: "Shell command to run when the schedule fires. The provider writes this command into `./.terraform-provider-host/schedules/<id>/run.sh`.",
			},
			"user": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "User whose crontab should contain the schedule. Defaults to the current Terraform user for `scope = \"user\"`, or `root` for `scope = \"system\"`.",
			},
			"scope": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Schedule scope. Supported values are `user` and `system`. `system` manages the root crontab and requires root privileges.",
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
				MarkdownDescription: "Whether the schedule should be present in the user's crontab.",
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
				MarkdownDescription: "Generated schedule runtime directory under `./.terraform-provider-host/schedules/<id>`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"script_path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Generated script path under `./.terraform-provider-host/schedules/<id>`.",
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

	if err := normalizeHostSchedulePlanTarget(&plan); err != nil {
		resp.Diagnostics.AddError("Invalid schedule target", err.Error())
		return
	}

	if !state.ID.IsNull() && !state.ID.IsUnknown() {
		plan.ID = state.ID
		status, err := hostScheduleStatusForProvider(HostScheduleSpec{
			ID:    state.ID.ValueString(),
			User:  plan.User.ValueString(),
			Scope: plan.Scope.ValueString(),
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
		spec, diags := hostScheduleSpecFromModel(ctx, plan)
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

	spec, diags := hostScheduleSpecFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := r.manager.UpsertSchedule(ctx, spec)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync schedule", err.Error())
		return
	}

	hydrateHostScheduleComputedState(&plan, status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
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

	spec, diags := hostScheduleSpecFromModel(ctx, state)
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
		resp.State.RemoveResource(ctx)
		return
	}

	hydrateHostScheduleComputedState(&state, status)
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

	spec, diags := hostScheduleSpecFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	stateSpec, stateDiags := hostScheduleSpecFromModel(ctx, state)
	resp.Diagnostics.Append(stateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if stateSpec.ID != "" && (stateSpec.User != spec.User || stateSpec.Scope != spec.Scope) {
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

	spec, diags := hostScheduleSpecFromModel(ctx, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.manager.DeleteSchedule(ctx, spec); err != nil {
		resp.Diagnostics.AddError("Failed to delete schedule", err.Error())
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
	model.User = types.StringValue(status.User)
	model.Scope = types.StringValue(status.Scope)
	model.Backend = types.StringValue(status.Backend)
	model.RuntimeDir = types.StringValue(status.RuntimeDir)
	model.ScriptPath = types.StringValue(status.ScriptPath)
	model.WorkingDirectoryResolved = optionalStringStateValue(status.WorkingDirectoryResolved)
	model.StdoutPathResolved = optionalStringStateValue(status.StdoutPathResolved)
	model.StderrPathResolved = optionalStringStateValue(status.StderrPathResolved)
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

func hostScheduleSpecFromModel(ctx context.Context, model HostScheduleResourceModel) (HostScheduleSpec, diag.Diagnostics) {
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
	if !model.User.IsNull() && !model.User.IsUnknown() {
		spec.User = model.User.ValueString()
	}
	if !model.Scope.IsNull() && !model.Scope.IsUnknown() {
		spec.Scope = model.Scope.ValueString()
	}
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
	if err := normalizeHostScheduleSpecTarget(&spec); err != nil {
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
		model.User.IsNull() || model.User.IsUnknown() ||
		model.Scope.IsNull() || model.Scope.IsUnknown() ||
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

func normalizeHostSchedulePlanTarget(model *HostScheduleResourceModel) error {
	scope := ""
	if !model.Scope.IsNull() && !model.Scope.IsUnknown() {
		scope = model.Scope.ValueString()
	}

	targetUser := ""
	if !model.User.IsNull() && !model.User.IsUnknown() {
		targetUser = model.User.ValueString()
	}

	normalizedScope, normalizedUser, err := normalizeHostScheduleTarget(scope, targetUser)
	if err != nil {
		return err
	}

	model.Scope = types.StringValue(normalizedScope)
	model.User = types.StringValue(normalizedUser)
	return nil
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
