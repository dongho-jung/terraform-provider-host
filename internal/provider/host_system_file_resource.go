package provider

import (
	"context"
	"fmt"
	"os"

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
	_ resource.Resource                = &HostSystemFileResource{}
	_ resource.ResourceWithConfigure   = &HostSystemFileResource{}
	_ resource.ResourceWithImportState = &HostSystemFileResource{}
	_ resource.ResourceWithModifyPlan  = &HostSystemFileResource{}
)

type HostSystemFileResource struct {
	homeDir string
	manager HostSystemFileManager
}

type HostSystemFileResourceModel struct {
	ID                     types.String `tfsdk:"id"`
	Destination            types.String `tfsdk:"destination"`
	Source                 types.String `tfsdk:"source"`
	Content                types.String `tfsdk:"content"`
	SourcePath             types.String `tfsdk:"source_path"`
	ChecksumSHA256         types.String `tfsdk:"checksum_sha256"`
	DeployedChecksumSHA256 types.String `tfsdk:"deployed_checksum_sha256"`
	Mode                   types.String `tfsdk:"mode"`
	Owner                  types.String `tfsdk:"owner"`
	Group                  types.String `tfsdk:"group"`
	AdoptExisting          types.Bool   `tfsdk:"adopt_existing"`
	DeleteOnDestroy        types.Bool   `tfsdk:"delete_on_destroy"`
}

func NewHostSystemFileResource() resource.Resource {
	return &HostSystemFileResource{}
}

func (r *HostSystemFileResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system_file"
}

func (r *HostSystemFileResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Atomically installs one root-owned privileged regular file at an absolute host path with a canonical group and a mode that is not writable by group or other users. The complete destination parent chain must already exist and consist of root-owned real directories that are not writable by group or other users. Exactly one of `source` or `content` is required. Source-backed files store only paths and SHA256 checksums in state; `content` is stored in Terraform state and is not a secret-safe input.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, equal to `destination`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"destination": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Clean absolute destination path for the installed regular file.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"source": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Local regular file to copy. `~` is expanded with the provider home directory and relative paths are resolved from the Terraform working directory. Mutually exclusive with `content`; source bytes are not stored in state.",
			},
			"content": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Literal file content. Mutually exclusive with `source`. This value is stored in plaintext Terraform state and must not be used for secrets.",
			},
			"source_path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute source path when `source` is configured.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"checksum_sha256": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Observed destination content SHA256. During planning this is the desired source or content SHA256, so source-file changes and destination drift produce a diff.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"deployed_checksum_sha256": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "SHA256 last installed or adopted by Terraform. Destructive deletion is refused when the destination no longer matches this checksum.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"mode": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("0644"),
				MarkdownDescription: "Destination permission mode as four octal digits. Group- or other-writable modes are rejected for privileged files.",
			},
			"owner": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("root"),
				MarkdownDescription: "Destination owner. Privileged system files are restricted to root ownership.",
			},
			"group": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(hostSystemRootGroup()),
				MarkdownDescription: "Canonical destination group name as resolved by the host account database. Defaults to the platform administrator group (`root` on Linux and `wheel` on macOS/FreeBSD).",
			},
			"adopt_existing": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Allow create to take ownership of a pre-existing destination and reconcile it. Defaults to false so existing system files require an explicit import or adoption decision.",
			},
			"delete_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Delete the destination on destroy. Defaults to false. When true, deletion is refused if content changed since the last successful install or adoption.",
			},
		},
	}
}

func (r *HostSystemFileResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	switch data := req.ProviderData.(type) {
	case HostProviderData:
		r.homeDir = data.HomeDir
		// Resolve sudo when an operation actually needs it. Configure runs before
		// resource dependencies, so a configure-time lookup would prevent a
		// package resource from bootstrapping sudo in the same apply.
		r.manager = NewCLIHostSystemFileManager("")
	case HostSystemFileManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected HostProviderData or HostSystemFileManager, got %T.", req.ProviderData))
	}
}

func (r *HostSystemFileResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan HostSystemFileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !hostSystemFilePlanReady(plan) {
		return
	}
	spec, sourcePath, err := hostSystemFileSpecFromModel(plan, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Invalid system file", err.Error())
		return
	}
	plan.ID = types.StringValue(spec.Destination)
	plan.ChecksumSHA256 = types.StringValue(hostSystemFileChecksum(spec.Content))
	if sourcePath == "" {
		plan.SourcePath = types.StringNull()
	} else {
		plan.SourcePath = types.StringValue(sourcePath)
	}
	if hostSystemFilePlanRequiresMutationWarning(ctx, req.State, plan, &resp.Diagnostics) {
		r.addPrivilegeWarning(&resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostSystemFileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostSystemFileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	spec, sourcePath, err := hostSystemFileSpecFromModel(plan, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Invalid system file", err.Error())
		return
	}
	if err := validateHostSystemFileSourcePlanChecksum(plan, spec, sourcePath); err != nil {
		resp.Diagnostics.AddError("System file source changed after planning", err.Error())
		return
	}
	existing, exists, err := r.manager.File(ctx, spec.Destination)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read system file", err.Error())
		return
	}
	if exists && !plan.AdoptExisting.ValueBool() {
		resp.Diagnostics.AddError("System file already exists", fmt.Sprintf("Destination %q already exists. Import it first or set adopt_existing = true to take ownership and reconcile it.", spec.Destination))
		return
	}
	if !exists || !hostSystemFileMatchesSpec(existing, spec) {
		if _, err := r.manager.InstallFile(ctx, spec); err != nil {
			resp.Diagnostics.AddError("Failed to install system file", err.Error())
			return
		}
	}
	state, exists, err := r.refreshState(ctx, plan, true)
	if err != nil || !exists {
		if err == nil {
			err = fmt.Errorf("system file %q was not found after install", spec.Destination)
		}
		resp.Diagnostics.AddError("Failed to read system file", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSystemFileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostSystemFileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	next, exists, err := r.refreshState(ctx, state, false)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read system file", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostSystemFileResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostSystemFileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	spec, sourcePath, err := hostSystemFileSpecFromModel(plan, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Invalid system file", err.Error())
		return
	}
	if err := validateHostSystemFileSourcePlanChecksum(plan, spec, sourcePath); err != nil {
		resp.Diagnostics.AddError("System file source changed after planning", err.Error())
		return
	}
	if _, err := r.manager.InstallFile(ctx, spec); err != nil {
		resp.Diagnostics.AddError("Failed to install system file", err.Error())
		return
	}
	state, exists, err := r.refreshState(ctx, plan, true)
	if err != nil || !exists {
		if err == nil {
			err = fmt.Errorf("system file %q was not found after install", spec.Destination)
		}
		resp.Diagnostics.AddError("Failed to read system file", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSystemFileResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostSystemFileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !state.DeleteOnDestroy.ValueBool() {
		return
	}
	if state.DeployedChecksumSHA256.IsNull() || state.DeployedChecksumSHA256.IsUnknown() {
		resp.Diagnostics.AddError("Failed to delete system file", "Refusing deletion because the last deployed checksum is unavailable.")
		return
	}
	if err := r.manager.DeleteFile(ctx, state.Destination.ValueString(), state.DeployedChecksumSHA256.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to delete system file", err.Error())
	}
}

func (r *HostSystemFileResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("destination"), req, resp)
}

func (r *HostSystemFileResource) refreshState(ctx context.Context, model HostSystemFileResourceModel, deployed bool) (HostSystemFileResourceModel, bool, error) {
	status, exists, err := r.manager.File(ctx, model.Destination.ValueString())
	if err != nil || !exists {
		return model, exists, err
	}
	model.ID = types.StringValue(status.Destination)
	model.Destination = types.StringValue(status.Destination)
	model.ChecksumSHA256 = types.StringValue(status.ChecksumSHA256)
	model.Mode = types.StringValue(formatHostSystemFileMode(status.Mode))
	model.Owner = types.StringValue(status.Owner)
	model.Group = types.StringValue(status.Group)
	if deployed || model.DeployedChecksumSHA256.IsNull() || model.DeployedChecksumSHA256.IsUnknown() {
		model.DeployedChecksumSHA256 = types.StringValue(status.ChecksumSHA256)
	}
	if !model.Source.IsNull() && !model.Source.IsUnknown() {
		resolved, resolveErr := expandHostPathWithHome(model.Source.ValueString(), r.homeDir)
		if resolveErr != nil {
			return model, false, resolveErr
		}
		model.SourcePath = types.StringValue(resolved)
	} else {
		model.SourcePath = types.StringNull()
	}
	if model.AdoptExisting.IsNull() || model.AdoptExisting.IsUnknown() {
		model.AdoptExisting = types.BoolValue(false)
	}
	if model.DeleteOnDestroy.IsNull() || model.DeleteOnDestroy.IsUnknown() {
		model.DeleteOnDestroy = types.BoolValue(false)
	}
	return model, true, nil
}

func (r *HostSystemFileResource) addPrivilegeWarning(diags *diag.Diagnostics) {
	if r.manager != nil && r.manager.NeedsPrivilegeEscalation() {
		addSudoPrivilegeWarningOnce(diags)
	}
}

func hostSystemFilePlanReady(model HostSystemFileResourceModel) bool {
	return !model.Destination.IsNull() && !model.Destination.IsUnknown() &&
		!model.Source.IsUnknown() && !model.Content.IsUnknown() &&
		!model.Mode.IsNull() && !model.Mode.IsUnknown() &&
		!model.Owner.IsNull() && !model.Owner.IsUnknown() &&
		!model.Group.IsNull() && !model.Group.IsUnknown() &&
		!model.AdoptExisting.IsNull() && !model.AdoptExisting.IsUnknown() &&
		!model.DeleteOnDestroy.IsNull() && !model.DeleteOnDestroy.IsUnknown()
}

func hostSystemFileSpecFromModel(model HostSystemFileResourceModel, homeDir string) (HostSystemFileSpec, string, error) {
	if err := validateHostSystemFileDestination(model.Destination.ValueString()); err != nil {
		return HostSystemFileSpec{}, "", err
	}
	mode, err := parseHostDirMode(model.Mode.ValueString())
	if err != nil {
		return HostSystemFileSpec{}, "", err
	}
	content, sourcePath, err := hostSystemFileContentFromModel(model, homeDir)
	if err != nil {
		return HostSystemFileSpec{}, "", err
	}
	spec := HostSystemFileSpec{
		Destination: model.Destination.ValueString(),
		Content:     content,
		Mode:        mode,
		Owner:       model.Owner.ValueString(),
		Group:       model.Group.ValueString(),
	}
	if err := validateHostSystemFileProtectedSpec(spec); err != nil {
		return HostSystemFileSpec{}, "", err
	}
	return spec, sourcePath, nil
}

func hostSystemFileContentFromModel(model HostSystemFileResourceModel, homeDir string) ([]byte, string, error) {
	hasSource := !model.Source.IsNull()
	hasContent := !model.Content.IsNull()
	if hasSource == hasContent {
		return nil, "", fmt.Errorf("exactly one of source or content must be configured")
	}
	if hasContent {
		return []byte(model.Content.ValueString()), "", nil
	}
	resolved, err := expandHostPathWithHome(model.Source.ValueString(), homeDir)
	if err != nil {
		return nil, "", fmt.Errorf("resolve source: %w", err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("read source %q: %w", resolved, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("source %q must be a regular file and must not be a symbolic link", resolved)
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("read source %q: %w", resolved, err)
	}
	return content, resolved, nil
}

func validateHostSystemFileSourcePlanChecksum(model HostSystemFileResourceModel, spec HostSystemFileSpec, sourcePath string) error {
	if model.Source.IsNull() {
		return nil
	}
	if model.ChecksumSHA256.IsNull() || model.ChecksumSHA256.IsUnknown() {
		return fmt.Errorf("planned SHA256 for source %q is unavailable; run terraform plan again before applying", sourcePath)
	}
	actualChecksum := hostSystemFileChecksum(spec.Content)
	plannedChecksum := model.ChecksumSHA256.ValueString()
	if actualChecksum != plannedChecksum {
		return fmt.Errorf("source %q changed after planning: current SHA256 %s does not match planned SHA256 %s; run terraform plan again", sourcePath, actualChecksum, plannedChecksum)
	}
	return nil
}

func hostSystemFilePlanRequiresMutationWarning(ctx context.Context, stateData tfsdk.State, plan HostSystemFileResourceModel, diags *diag.Diagnostics) bool {
	if stateData.Raw.IsNull() {
		return true
	}
	var state HostSystemFileResourceModel
	diags.Append(stateData.Get(ctx, &state)...)
	if diags.HasError() {
		return false
	}
	return plan.Destination != state.Destination || plan.Source != state.Source || plan.Content != state.Content ||
		plan.ChecksumSHA256 != state.ChecksumSHA256 || plan.Mode != state.Mode || plan.Owner != state.Owner || plan.Group != state.Group
}
