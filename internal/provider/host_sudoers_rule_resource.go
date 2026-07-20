package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &HostSudoersRuleResource{}
	_ resource.ResourceWithConfigure   = &HostSudoersRuleResource{}
	_ resource.ResourceWithImportState = &HostSudoersRuleResource{}
	_ resource.ResourceWithModifyPlan  = &HostSudoersRuleResource{}
)

type HostSudoersRuleResource struct {
	manager    HostSystemFileManager
	validator  HostSudoersValidator
	targetUser string
}

type HostSudoersRuleBackend struct {
	Manager   HostSystemFileManager
	Validator HostSudoersValidator
}

type HostSudoersRuleResourceModel struct {
	ID                     types.String `tfsdk:"id"`
	Name                   types.String `tfsdk:"name"`
	User                   types.String `tfsdk:"user"`
	Commands               types.Set    `tfsdk:"commands"`
	RunAs                  types.String `tfsdk:"run_as"`
	NoPassword             types.Bool   `tfsdk:"nopasswd"`
	RenderedContent        types.String `tfsdk:"rendered_content"`
	Path                   types.String `tfsdk:"path"`
	ChecksumSHA256         types.String `tfsdk:"checksum_sha256"`
	DeployedChecksumSHA256 types.String `tfsdk:"deployed_checksum_sha256"`
	Mode                   types.String `tfsdk:"mode"`
	Owner                  types.String `tfsdk:"owner"`
	Group                  types.String `tfsdk:"group"`
	AdoptExisting          types.Bool   `tfsdk:"adopt_existing"`
	DeleteOnDestroy        types.Bool   `tfsdk:"delete_on_destroy"`
}

func NewHostSudoersRuleResource() resource.Resource {
	return &HostSudoersRuleResource{}
}

func (r *HostSudoersRuleResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sudoers_rule"
}

func (r *HostSudoersRuleResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: fmt.Sprintf("Manages one structured `%s` rule with fixed root:%s ownership and mode 0440. Configure a literal local `user` and one or more literal absolute `commands`. Every command is restricted to an exact no-argument invocation. Every create and update must pass strict `visudo` syntax validation before an atomic install.", hostSudoersRuleDirectory(), hostSystemRootGroup()),
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, equal to `name`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: fmt.Sprintf("Safe sudoers drop-in filename under `%s`. Dots are rejected because sudo commonly skips includedir filenames containing a dot.", hostSudoersRuleDirectory()),
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"user": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Literal local user granted the commands. Sudoers aliases, groups, netgroups, numeric IDs, and the reserved ALL token are intentionally unsupported.",
			},
			"commands": schema.SetAttribute{
				ElementType:         types.StringType,
				Required:            true,
				MarkdownDescription: "Non-empty set of clean, literal absolute executable paths. Each path is rendered with sudoers' empty-argument marker, so only an invocation with no command-line arguments is authorized. Arguments and sudoers wildcard or escape metacharacters are intentionally unsupported.",
			},
			"run_as": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("root"),
				MarkdownDescription: "User the structured commands run as.",
			},
			"nopasswd": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Render NOPASSWD for the structured rule. Set false to require normal sudo authentication.",
			},
			"rendered_content": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Canonical content validated and installed by the resource.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: fmt.Sprintf("Absolute drop-in path under `%s`.", hostSudoersRuleDirectory()),
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"checksum_sha256": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Observed drop-in content SHA256. During planning this is the desired rendered-content SHA256.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"deployed_checksum_sha256": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "SHA256 last installed or adopted by Terraform. Deletion is refused after unmanaged content changes.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"mode": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Observed mode, planned as the required value 0440.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"owner": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Observed owner, planned as root.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"group": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Observed group, planned as the platform administrator group (`root` on Linux and `wheel` on macOS/FreeBSD).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"adopt_existing": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Allow create to take ownership of a pre-existing drop-in and reconcile it. Defaults to false so existing privilege rules require an explicit import or adoption decision.",
			},
			"delete_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Remove the privilege rule on destroy. Deletion is refused if content changed since the last successful install or adoption.",
			},
		},
	}
}

func (r *HostSudoersRuleResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	switch data := req.ProviderData.(type) {
	case HostProviderData:
		r.targetUser = data.TargetUser
		r.manager = NewCLIHostSystemFileManager("")
		r.validator = NewCLIHostSudoersValidator("")
	case HostSudoersRuleBackend:
		r.manager = data.Manager
		r.validator = data.Validator
	default:
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected HostProviderData or HostSudoersRuleBackend, got %T.", req.ProviderData))
	}
	if r.manager == nil || r.validator == nil {
		resp.Diagnostics.AddError("Sudoers backend unavailable", "host_sudoers_rule requires a system file manager and visudo validator.")
	}
}

func (r *HostSudoersRuleResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan HostSudoersRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !hostSudoersRulePlanReady(plan) {
		return
	}
	if err := r.validateTargetUser(plan); err != nil {
		resp.Diagnostics.AddError("Invalid sudoers rule", err.Error())
		return
	}
	spec, rendered, err := hostSudoersRuleSpecFromModel(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid sudoers rule", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.Name.ValueString())
	plan.Path = types.StringValue(spec.Destination)
	plan.RenderedContent = types.StringValue(string(rendered))
	plan.ChecksumSHA256 = types.StringValue(hostSystemFileChecksum(rendered))
	plan.Mode = types.StringValue("0440")
	plan.Owner = types.StringValue("root")
	plan.Group = types.StringValue(hostSystemRootGroup())
	if hostSudoersRulePlanRequiresMutationWarning(ctx, req.State, plan, &resp.Diagnostics) {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostSudoersRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostSudoersRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.validateTargetUser(plan); err != nil {
		resp.Diagnostics.AddError("Invalid sudoers rule", err.Error())
		return
	}
	spec, rendered, err := hostSudoersRuleSpecFromModel(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid sudoers rule", err.Error())
		return
	}
	if err := validateHostSudoersCommandFiles(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Unsafe sudoers command", err.Error())
		return
	}
	if err := r.validator.Validate(ctx, rendered); err != nil {
		resp.Diagnostics.AddError("Invalid sudoers rule", err.Error())
		return
	}
	existing, exists, err := r.manager.File(ctx, spec.Destination)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read sudoers rule", err.Error())
		return
	}
	if exists && !plan.AdoptExisting.ValueBool() {
		resp.Diagnostics.AddError("Sudoers rule already exists", fmt.Sprintf("Drop-in %q already exists. Import it first or set adopt_existing = true to take ownership and reconcile it.", spec.Destination))
		return
	}
	if !exists || !hostSystemFileMatchesSpec(existing, spec) {
		if _, err := r.manager.InstallFile(ctx, spec); err != nil {
			resp.Diagnostics.AddError("Failed to install sudoers rule", err.Error())
			return
		}
	}
	plan.RenderedContent = types.StringValue(string(rendered))
	state, exists, err := r.refreshState(ctx, plan, true)
	if err != nil || !exists {
		if err == nil {
			err = fmt.Errorf("sudoers rule %q was not found after install", plan.Name.ValueString())
		}
		resp.Diagnostics.AddError("Failed to read sudoers rule", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSudoersRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostSudoersRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.validateTargetUser(state); err != nil {
		resp.Diagnostics.AddError("Invalid sudoers rule", err.Error())
		return
	}
	next, exists, err := r.refreshState(ctx, state, false)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read sudoers rule", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}
	if err := validateHostSudoersCommandFiles(ctx, state); err != nil {
		resp.Diagnostics.AddWarning(
			"Unsafe sudoers command drift",
			fmt.Sprintf("A configured sudoers command no longer meets the protected-wrapper requirements: %s. Terraform can still update or destroy this rule, but create and update refuse to install it until every command is safe.", err),
		)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostSudoersRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostSudoersRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.validateTargetUser(plan); err != nil {
		resp.Diagnostics.AddError("Invalid sudoers rule", err.Error())
		return
	}
	spec, rendered, err := hostSudoersRuleSpecFromModel(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid sudoers rule", err.Error())
		return
	}
	if err := validateHostSudoersCommandFiles(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Unsafe sudoers command", err.Error())
		return
	}
	if err := r.validator.Validate(ctx, rendered); err != nil {
		resp.Diagnostics.AddError("Invalid sudoers rule", err.Error())
		return
	}
	if _, err := r.manager.InstallFile(ctx, spec); err != nil {
		resp.Diagnostics.AddError("Failed to install sudoers rule", err.Error())
		return
	}
	plan.RenderedContent = types.StringValue(string(rendered))
	state, exists, err := r.refreshState(ctx, plan, true)
	if err != nil || !exists {
		if err == nil {
			err = fmt.Errorf("sudoers rule %q was not found after install", plan.Name.ValueString())
		}
		resp.Diagnostics.AddError("Failed to read sudoers rule", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSudoersRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostSudoersRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !state.DeleteOnDestroy.ValueBool() {
		return
	}
	if state.DeployedChecksumSHA256.IsNull() || state.DeployedChecksumSHA256.IsUnknown() {
		resp.Diagnostics.AddError("Failed to delete sudoers rule", "Refusing deletion because the last deployed checksum is unavailable.")
		return
	}
	if err := validateHostSudoersRuleName(state.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to delete sudoers rule", fmt.Sprintf("Refusing deletion with an invalid state name: %s", err))
		return
	}
	if err := r.manager.DeleteFile(ctx, hostSudoersRulePath(state.Name.ValueString()), state.DeployedChecksumSHA256.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to delete sudoers rule", err.Error())
	}
}

func (r *HostSudoersRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *HostSudoersRuleResource) refreshState(ctx context.Context, model HostSudoersRuleResourceModel, deployed bool) (HostSudoersRuleResourceModel, bool, error) {
	if err := validateHostSudoersRuleName(model.Name.ValueString()); err != nil {
		return model, false, err
	}
	destination := hostSudoersRulePath(model.Name.ValueString())
	status, exists, err := r.manager.File(ctx, destination)
	if err != nil || !exists {
		return model, exists, err
	}
	model.ID = types.StringValue(model.Name.ValueString())
	model.Path = types.StringValue(destination)
	model.ChecksumSHA256 = types.StringValue(status.ChecksumSHA256)
	model.Mode = types.StringValue(formatHostSystemFileMode(status.Mode))
	model.Owner = types.StringValue(status.Owner)
	model.Group = types.StringValue(status.Group)
	if deployed || model.DeployedChecksumSHA256.IsNull() || model.DeployedChecksumSHA256.IsUnknown() {
		model.DeployedChecksumSHA256 = types.StringValue(status.ChecksumSHA256)
	}
	if model.AdoptExisting.IsNull() || model.AdoptExisting.IsUnknown() {
		model.AdoptExisting = types.BoolValue(false)
	}
	if model.DeleteOnDestroy.IsNull() || model.DeleteOnDestroy.IsUnknown() {
		model.DeleteOnDestroy = types.BoolValue(true)
	}
	return model, true, nil
}

func (r *HostSudoersRuleResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	if r.manager != nil && r.manager.NeedsPrivilegeEscalation() {
		addSudoPrivilegeWarningOnce(diags)
	}
}

func (r *HostSudoersRuleResource) validateTargetUser(model HostSudoersRuleResourceModel) error {
	if r.targetUser != "" && !model.User.IsNull() && !model.User.IsUnknown() && model.User.ValueString() != r.targetUser {
		return fmt.Errorf("sudoers user %q must match provider target_user %q", model.User.ValueString(), r.targetUser)
	}
	return nil
}

func hostSudoersRulePlanReady(model HostSudoersRuleResourceModel) bool {
	return !model.Name.IsNull() && !model.Name.IsUnknown() &&
		!model.User.IsNull() && !model.User.IsUnknown() &&
		!model.Commands.IsNull() && !model.Commands.IsUnknown() &&
		!model.RunAs.IsNull() && !model.RunAs.IsUnknown() &&
		!model.NoPassword.IsNull() && !model.NoPassword.IsUnknown() &&
		!model.AdoptExisting.IsNull() && !model.AdoptExisting.IsUnknown() &&
		!model.DeleteOnDestroy.IsNull() && !model.DeleteOnDestroy.IsUnknown()
}

func hostSudoersRuleSpecFromModel(ctx context.Context, model HostSudoersRuleResourceModel) (HostSystemFileSpec, []byte, error) {
	if err := validateHostSudoersRuleName(model.Name.ValueString()); err != nil {
		return HostSystemFileSpec{}, nil, err
	}
	rendered, err := renderHostSudoersRule(ctx, model)
	if err != nil {
		return HostSystemFileSpec{}, nil, err
	}
	return HostSystemFileSpec{
		Destination: hostSudoersRulePath(model.Name.ValueString()),
		Content:     rendered,
		Mode:        0o440,
		Owner:       "root",
		Group:       hostSystemRootGroup(),
	}, rendered, nil
}

func renderHostSudoersRule(ctx context.Context, model HostSudoersRuleResourceModel) ([]byte, error) {
	if model.User.IsNull() || model.Commands.IsNull() || len(model.Commands.Elements()) == 0 {
		return nil, fmt.Errorf("configure both user and a non-empty commands set")
	}
	user := model.User.ValueString()
	if err := validateHostSudoersPrincipal(user); err != nil {
		return nil, fmt.Errorf("invalid sudoers user: %w", err)
	}
	runAs := model.RunAs.ValueString()
	if err := validateHostSudoersPrincipal(runAs); err != nil {
		return nil, fmt.Errorf("invalid run_as user: %w", err)
	}
	var commands []string
	diags := model.Commands.ElementsAs(ctx, &commands, false)
	if diags.HasError() {
		return nil, fmt.Errorf("decode sudoers commands: %s", diags.Errors()[0].Detail())
	}
	for index, command := range commands {
		if err := validateHostSudoersCommand(command); err != nil {
			return nil, err
		}
		// An omitted argument list in sudoers permits any arguments. The quoted
		// empty argument marker restricts the grant to an exact no-argument
		// invocation, which is the only shape this structured resource accepts.
		commands[index] = command + ` ""`
	}
	sort.Strings(commands)
	tag := "PASSWD"
	if model.NoPassword.ValueBool() {
		tag = "NOPASSWD"
	}
	return []byte(fmt.Sprintf("%s ALL=(%s) %s: %s\n", user, runAs, tag, strings.Join(commands, ", "))), nil
}

func validateHostSudoersRuleName(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("sudoers rule name must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(name, "/\\\x00\r\n\t ,~.") || strings.HasPrefix(name, "-") {
		return fmt.Errorf("sudoers rule name %q is invalid; use letters, numbers, underscores, and hyphens without a leading hyphen", name)
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return fmt.Errorf("sudoers rule name %q contains unsupported character %q", name, r)
		}
	}
	return nil
}

func validateHostSudoersCommand(command string) error {
	if strings.TrimSpace(command) != command || command == "" {
		return fmt.Errorf("sudoers command must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(command, "\x00\r\n\t ,\\") || !filepath.IsAbs(command) || filepath.Clean(command) != command {
		return fmt.Errorf("sudoers command %q must be a clean absolute executable path without whitespace, commas, or backslashes", command)
	}
	for _, character := range command {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '/' || character == '_' || character == '-' ||
			character == '.' || character == '+' {
			continue
		}
		return fmt.Errorf("sudoers command %q contains metacharacter %q; structured commands accept only literal portable path characters", command, character)
	}
	return nil
}

func validateHostSudoersCommandFiles(ctx context.Context, model HostSudoersRuleResourceModel) error {
	if model.Commands.IsNull() || model.Commands.IsUnknown() {
		return nil
	}
	var commands []string
	diags := model.Commands.ElementsAs(ctx, &commands, false)
	if diags.HasError() {
		return fmt.Errorf("decode sudoers commands for safety validation: %s", diags.Errors()[0].Detail())
	}
	for _, command := range commands {
		if err := validateHostSudoersCommandFile(command); err != nil {
			return fmt.Errorf("sudoers command %q is unsafe: %w", command, err)
		}
	}
	return nil
}

func validateHostSudoersCommandFile(command string) error {
	if err := validateHostSudoersCommand(command); err != nil {
		return err
	}
	if err := validateHostSystemFileProtectedParents(command); err != nil {
		return err
	}
	info, err := os.Lstat(command)
	if err != nil {
		return fmt.Errorf("inspect executable: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("must be a real regular file and must not be a symbolic link")
	}
	uid, _, err := hostSystemFileNumericOwnership(info)
	if err != nil {
		return fmt.Errorf("inspect executable ownership: %w", err)
	}
	if uid != "0" {
		return fmt.Errorf("must be root-owned")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("must not be writable by group or other users")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("must have at least one executable permission bit")
	}
	return nil
}

func validateHostSudoersPrincipal(principal string) error {
	if err := validateHostUserName(principal); err != nil {
		return err
	}
	if strings.EqualFold(principal, "ALL") {
		return fmt.Errorf("principal %q is reserved by sudoers", principal)
	}
	for index, character := range principal {
		if character >= 'a' && character <= 'z' ||
			(index > 0 && character >= '0' && character <= '9') ||
			character == '_' || character == '-' || character == '.' {
			if index == 0 && (character == '-' || character == '.') {
				break
			}
			continue
		}
		return fmt.Errorf("principal %q is not a portable literal username; use a lowercase letter or underscore first, followed by lowercase letters, digits, underscores, hyphens, or dots", principal)
	}
	if principal[0] == '-' || principal[0] == '.' {
		return fmt.Errorf("principal %q must not start with a sudoers metacharacter", principal)
	}
	return nil
}

func hostSudoersRulePath(name string) string {
	return filepath.Join(hostSudoersRuleDirectory(), name)
}

func hostSudoersRuleDirectory() string {
	if runtime.GOOS == "freebsd" {
		return "/usr/local/etc/sudoers.d"
	}
	if runtime.GOOS == "darwin" {
		return "/private/etc/sudoers.d"
	}
	return "/etc/sudoers.d"
}

func hostSudoersRulePlanRequiresMutationWarning(ctx context.Context, stateData tfsdk.State, plan HostSudoersRuleResourceModel, diags *diag.Diagnostics) bool {
	if stateData.Raw.IsNull() {
		return true
	}
	var state HostSudoersRuleResourceModel
	diags.Append(stateData.Get(ctx, &state)...)
	if diags.HasError() {
		return false
	}
	return plan.Name != state.Name || plan.RenderedContent != state.RenderedContent || plan.ChecksumSHA256 != state.ChecksumSHA256 ||
		plan.Mode != state.Mode || plan.Owner != state.Owner || plan.Group != state.Group
}
