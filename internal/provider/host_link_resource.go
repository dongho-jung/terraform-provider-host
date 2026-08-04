package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &HostLinkResource{}
	_ resource.ResourceWithConfigure   = &HostLinkResource{}
	_ resource.ResourceWithImportState = &HostLinkResource{}
	_ resource.ResourceWithModifyPlan  = &HostLinkResource{}
)

type HostLinkResource struct {
	homeDir    string
	runtimeDir string
}

type HostLinkResourceModel struct {
	ID              types.String `tfsdk:"id"`
	Source          types.String `tfsdk:"source"`
	StageSource     types.Bool   `tfsdk:"stage_source"`
	Destination     types.String `tfsdk:"destination"`
	SourcePath      types.String `tfsdk:"source_path"`
	SourceDigest    types.String `tfsdk:"source_digest"`
	DestinationPath types.String `tfsdk:"destination_path"`
}

func NewHostLinkResource() resource.Resource {
	return &HostLinkResource{}
}

func (r *HostLinkResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_link"
}

func (r *HostLinkResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(HostProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected HostProviderData, got %T.", req.ProviderData))
		return
	}
	if !requireHostUserScope(data, "host_link", &resp.Diagnostics) {
		return
	}
	r.homeDir = data.HomeDir
	r.runtimeDir = data.RuntimeDir
}

func (r *HostLinkResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a symbolic link from a destination host path to a source file or directory.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, equal to `destination`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"source": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Source file or directory the symbolic link points to. Absolute paths are used as-is, `~` is expanded to the provider `home_dir`, and relative paths are resolved from the Terraform working directory.",
			},
			"stage_source": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Copy the source into a content-addressed directory under the provider `runtime_dir` and point the symbolic link at that stable copy. This isolates managed links from temporary Terraform worktrees; source content changes take effect on the next apply instead of immediately. Each apply publishes the new copy by atomically repointing a fixed `current` indirection, so `source_path` stays constant and `source_digest` is the only attribute that tracks content.",
			},
			"destination": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Destination host path where the symbolic link should exist. `~` is expanded to the provider `home_dir`.",
			},
			"source_path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute source path currently stored in the symbolic link. When `stage_source` is true this is a fixed path under the provider `runtime_dir` that stays the same across source content changes, so only `source_digest` moves in the plan.",
			},
			"source_digest": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Content and permission SHA256 for a staged source. Null when `stage_source` is false.",
			},
			"destination_path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute symbolic link destination path.",
			},
		},
	}
}

func (r *HostLinkResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostLinkResourceModel
	var state HostLinkResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	}
	if resp.Diagnostics.HasError() || plan.Source.IsNull() || plan.Source.IsUnknown() || plan.Destination.IsNull() || plan.Destination.IsUnknown() {
		return
	}

	resolved, err := resolveHostLinkModel(plan, r.homeDir, r.runtimeDir)
	if err != nil {
		resp.Diagnostics.AddError("Invalid host link", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.Destination.ValueString())
	plan.SourcePath = types.StringValue(resolved.Link.SourcePath)
	if resolved.SourceDigest == "" {
		plan.SourceDigest = types.StringNull()
	} else {
		plan.SourceDigest = types.StringValue(resolved.SourceDigest)
	}
	plan.DestinationPath = types.StringValue(resolved.Link.DestinationPath)
	requireReplaceIfResolvedPathChanged(req, resp, path.Root("destination"), state.DestinationPath, resolved.Link.DestinationPath)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostLinkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostLinkResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncLink(plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync host link", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostLinkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostLinkResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	destinationPath, err := resolveHostLinkDestinationForHome(state.Destination.ValueString(), r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Invalid host link state", err.Error())
		return
	}

	actualSource, exists, err := readHostLinkSource(destinationPath)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read host link", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(state.Destination.ValueString())
	state.SourcePath = types.StringValue(actualSource)
	state.DestinationPath = types.StringValue(destinationPath)
	if state.StageSource.IsNull() || state.StageSource.IsUnknown() {
		state.StageSource = types.BoolValue(false)
	}
	if state.StageSource.ValueBool() {
		digest, digestErr := hostLinkStagedSourceDigest(actualSource)
		if digestErr != nil {
			state.SourceDigest = types.StringNull()
		} else {
			state.SourceDigest = types.StringValue(digest)
		}
	} else {
		state.SourceDigest = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostLinkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostLinkResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncLink(plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync host link", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostLinkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostLinkResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	link, err := hostLinkSpecFromModelForHome(state, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Invalid host link state", err.Error())
		return
	}

	if err := deleteHostLink(link.DestinationPath); err != nil {
		resp.Diagnostics.AddError("Failed to delete host link", err.Error())
		return
	}
	if !state.StageSource.IsNull() && !state.StageSource.IsUnknown() && state.StageSource.ValueBool() {
		stageRoot, stageErr := hostLinkStageRoot(r.runtimeDir, link.DestinationPath)
		if stageErr != nil {
			resp.Diagnostics.AddError("Failed to delete staged host link", stageErr.Error())
			return
		}
		if stageErr := removeHostLinkStageRoot(stageRoot); stageErr != nil {
			resp.Diagnostics.AddError("Failed to delete staged host link", stageErr.Error())
		}
	}
}

func (r *HostLinkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("destination"), req, resp)
	if resp.Diagnostics.HasError() {
		return
	}

	destinationPath, err := resolveHostLinkDestinationForHome(req.ID, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import host link", err.Error())
		return
	}
	sourcePath, exists, err := readHostLinkSource(destinationPath)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import host link", err.Error())
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Failed to import host link", fmt.Sprintf("Symbolic link %q does not exist.", req.ID))
		return
	}

	// Hydrate the required source attribute before the first Read. Import only
	// supplies a destination, while Read needs both sides to resolve the link.
	resp.Diagnostics.Append(resp.State.SetAttribute(
		ctx,
		path.Root("source"),
		types.StringValue(sourcePath),
	)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(
		ctx,
		path.Root("stage_source"),
		types.BoolValue(false),
	)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(
		ctx,
		path.Root("source_digest"),
		types.StringNull(),
	)...)
}

func (r *HostLinkResource) syncLink(model HostLinkResourceModel) (HostLinkResourceModel, error) {
	resolved, err := resolveHostLinkModel(model, r.homeDir, r.runtimeDir)
	if err != nil {
		return model, err
	}

	if resolved.StageRoot != "" {
		if err := stageHostLinkSource(resolved.OriginalSourcePath, resolved.StagedVersionPath, resolved.SourceDigest); err != nil {
			return model, err
		}
		if err := writeHostLink(resolved.Link.SourcePath, resolved.StagedVersionPath); err != nil {
			return model, err
		}
	}
	if err := writeHostLink(resolved.Link.DestinationPath, resolved.Link.SourcePath); err != nil {
		return model, err
	}
	if resolved.StageRoot == "" && r.runtimeDir != "" {
		stageRoot, stageErr := hostLinkStageRoot(r.runtimeDir, resolved.Link.DestinationPath)
		if stageErr != nil {
			return model, stageErr
		}
		if stageErr := removeHostLinkStageRoot(stageRoot); stageErr != nil {
			return model, stageErr
		}
	} else if err := cleanupHostLinkStages(resolved.StageRoot, resolved.SourceDigest); err != nil {
		return model, err
	}

	model.ID = types.StringValue(model.Destination.ValueString())
	model.SourcePath = types.StringValue(resolved.Link.SourcePath)
	if resolved.SourceDigest == "" {
		model.SourceDigest = types.StringNull()
	} else {
		model.SourceDigest = types.StringValue(resolved.SourceDigest)
	}
	model.DestinationPath = types.StringValue(resolved.Link.DestinationPath)
	return model, nil
}

type hostLinkSpec struct {
	DestinationPath string
	SourcePath      string
}

type resolvedHostLink struct {
	Link               hostLinkSpec
	OriginalSourcePath string
	SourceDigest       string
	StageRoot          string
	StagedVersionPath  string
}

func resolveHostLinkModel(model HostLinkResourceModel, homeDir string, runtimeDir string) (resolvedHostLink, error) {
	link, err := hostLinkSpecFromModelForHome(model, homeDir)
	if err != nil {
		return resolvedHostLink{}, err
	}
	if err := ensureHostLinkSourceExists(link.SourcePath); err != nil {
		return resolvedHostLink{}, err
	}

	resolved := resolvedHostLink{
		Link:               link,
		OriginalSourcePath: link.SourcePath,
	}
	if model.StageSource.IsNull() || model.StageSource.IsUnknown() || !model.StageSource.ValueBool() {
		return resolved, nil
	}

	digest, err := hostLinkSourceDigest(link.SourcePath)
	if err != nil {
		return resolvedHostLink{}, fmt.Errorf("digest source: %w", err)
	}
	stageRoot, err := hostLinkStageRoot(runtimeDir, link.DestinationPath)
	if err != nil {
		return resolvedHostLink{}, fmt.Errorf("resolve staged source: %w", err)
	}

	resolved.SourceDigest = digest
	resolved.StageRoot = stageRoot
	resolved.StagedVersionPath = hostLinkStagePath(stageRoot, digest)
	resolved.Link.SourcePath = hostLinkStageCurrentPath(stageRoot)
	return resolved, nil
}

func hostLinkSpecFromModelForHome(model HostLinkResourceModel, homeDir string) (hostLinkSpec, error) {
	if model.Destination.IsNull() || model.Destination.IsUnknown() {
		return hostLinkSpec{}, fmt.Errorf("destination must be known")
	}
	if model.Source.IsNull() || model.Source.IsUnknown() {
		return hostLinkSpec{}, fmt.Errorf("source must be known")
	}

	destinationPath, err := resolveHostLinkDestinationForHome(model.Destination.ValueString(), homeDir)
	if err != nil {
		return hostLinkSpec{}, fmt.Errorf("invalid destination: %w", err)
	}
	sourcePath, err := resolveHostLinkSourceForHome(model.Source.ValueString(), homeDir)
	if err != nil {
		return hostLinkSpec{}, fmt.Errorf("invalid source: %w", err)
	}

	return hostLinkSpec{
		DestinationPath: destinationPath,
		SourcePath:      sourcePath,
	}, nil
}

func resolveHostLinkDestinationForHome(value string, homeDir string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("destination must be non-empty")
	}
	if strings.Contains(value, "\x00") {
		return "", fmt.Errorf("destination must not contain NUL bytes")
	}

	if strings.HasPrefix(value, "~") {
		return expandHostPathWithHome(value, homeDir)
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current working directory: %w", err)
	}
	return filepath.Clean(filepath.Join(workingDir, value)), nil
}

func resolveHostLinkSourceForHome(value string, homeDir string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("source must be non-empty")
	}
	if strings.Contains(value, "\x00") {
		return "", fmt.Errorf("source must not contain NUL bytes")
	}

	if strings.HasPrefix(value, "~") {
		return expandHostPathWithHome(value, homeDir)
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current working directory: %w", err)
	}
	return filepath.Clean(filepath.Join(workingDir, value)), nil
}

func ensureHostLinkSourceExists(sourcePath string) error {
	if _, err := os.Stat(sourcePath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source %q does not exist", sourcePath)
		}
		return fmt.Errorf("read source %q: %w", sourcePath, err)
	}

	return nil
}

func writeHostLink(destinationPath string, sourcePath string) error {
	parent := filepath.Dir(destinationPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create link parent directory %q: %w", parent, err)
	}

	actualSource, exists, err := readHostLinkSource(destinationPath)
	if err != nil {
		return err
	}
	if exists {
		if sameHostLinkPath(actualSource, sourcePath) {
			return nil
		}
	}

	tempDir, err := os.MkdirTemp(parent, ".terraform-provider-host-link-*")
	if err != nil {
		return fmt.Errorf("create temporary link directory for %q: %w", destinationPath, err)
	}
	defer os.RemoveAll(tempDir)

	tempLink := filepath.Join(tempDir, "link")
	if err := os.Symlink(sourcePath, tempLink); err != nil {
		return fmt.Errorf("create temporary symbolic link %q -> %q: %w", tempLink, sourcePath, err)
	}
	if err := os.Rename(tempLink, destinationPath); err != nil {
		if !exists || runtime.GOOS != "windows" {
			return fmt.Errorf("atomically replace symbolic link %q -> %q: %w", destinationPath, sourcePath, err)
		}
		if removeErr := os.Remove(destinationPath); removeErr != nil {
			return fmt.Errorf("remove stale symbolic link %q: %w", destinationPath, removeErr)
		}
		if renameErr := os.Rename(tempLink, destinationPath); renameErr != nil {
			return fmt.Errorf("replace symbolic link %q -> %q: %w", destinationPath, sourcePath, renameErr)
		}
	}

	return nil
}

func readHostLinkSource(destinationPath string) (string, bool, error) {
	info, err := os.Lstat(destinationPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read link destination %q: %w", destinationPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", false, fmt.Errorf("destination %q already exists and is not a symbolic link; move it aside before applying host_link", destinationPath)
	}

	rawSource, err := os.Readlink(destinationPath)
	if err != nil {
		return "", false, fmt.Errorf("read symbolic link %q: %w", destinationPath, err)
	}
	sourcePath := rawSource
	if !filepath.IsAbs(sourcePath) {
		sourcePath = filepath.Join(filepath.Dir(destinationPath), sourcePath)
	}

	return filepath.Clean(sourcePath), true, nil
}

func deleteHostLink(destinationPath string) error {
	info, err := os.Lstat(destinationPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read link destination %q: %w", destinationPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("destination %q exists and is not a symbolic link; refusing to remove it", destinationPath)
	}

	if err := os.Remove(destinationPath); err != nil {
		return fmt.Errorf("remove symbolic link %q: %w", destinationPath, err)
	}

	return nil
}

func sameHostLinkPath(left string, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
