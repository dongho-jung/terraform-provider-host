package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                 = &HostLinkResource{}
	_ resource.ResourceWithImportState  = &HostLinkResource{}
	_ resource.ResourceWithModifyPlan   = &HostLinkResource{}
	_ resource.ResourceWithUpgradeState = &HostLinkResource{}
)

type HostLinkResource struct {
}

type HostLinkResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Source      types.String `tfsdk:"source"`
	Destination types.String `tfsdk:"destination"`
	SourcePath  types.String `tfsdk:"source_path"`
}

type hostLinkResourceModelV0 struct {
	ID         types.String `tfsdk:"id"`
	Path       types.String `tfsdk:"path"`
	Target     types.String `tfsdk:"target"`
	TargetPath types.String `tfsdk:"target_path"`
}

func NewHostLinkResource() resource.Resource {
	return &HostLinkResource{}
}

func (r *HostLinkResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_link"
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
				MarkdownDescription: "Source file or directory the symbolic link points to. Absolute paths are used as-is, `~` is expanded to the current user's home directory, and relative paths are resolved from the Terraform working directory.",
			},
			"destination": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Destination host path where the symbolic link should exist. `~` is expanded to the current user's home directory.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"source_path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute source path currently stored in the symbolic link.",
			},
		},
	}
}

func (r *HostLinkResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: hostLinkResourceV0Schema(),
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior hostLinkResourceModelV0
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}

				upgraded := HostLinkResourceModel{
					ID:          prior.ID,
					Source:      prior.Target,
					Destination: prior.Path,
					SourcePath:  prior.TargetPath,
				}

				resp.Diagnostics.Append(resp.State.Set(ctx, &upgraded)...)
			},
		},
	}
}

func hostLinkResourceV0Schema() *schema.Schema {
	return &schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"path": schema.StringAttribute{
				Required: true,
			},
			"target": schema.StringAttribute{
				Required: true,
			},
			"target_path": schema.StringAttribute{
				Computed: true,
			},
		},
	}
}

func (r *HostLinkResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostLinkResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Source.IsNull() || plan.Source.IsUnknown() || plan.Destination.IsNull() || plan.Destination.IsUnknown() {
		return
	}

	link, err := hostLinkSpecFromModel(plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid host link", err.Error())
		return
	}
	if err := ensureHostLinkSourceExists(link.SourcePath); err != nil {
		resp.Diagnostics.AddError("Invalid host link source", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.Destination.ValueString())
	plan.SourcePath = types.StringValue(link.SourcePath)
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

	link, err := hostLinkSpecFromModel(state)
	if err != nil {
		resp.Diagnostics.AddError("Invalid host link state", err.Error())
		return
	}

	actualSource, exists, err := readHostLinkSource(link.DestinationPath)
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

	link, err := hostLinkSpecFromModel(state)
	if err != nil {
		resp.Diagnostics.AddError("Invalid host link state", err.Error())
		return
	}

	if err := deleteHostLink(link.DestinationPath); err != nil {
		resp.Diagnostics.AddError("Failed to delete host link", err.Error())
	}
}

func (r *HostLinkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("destination"), req, resp)
}

func (r *HostLinkResource) syncLink(model HostLinkResourceModel) (HostLinkResourceModel, error) {
	link, err := hostLinkSpecFromModel(model)
	if err != nil {
		return model, err
	}

	if err := ensureHostLinkSourceExists(link.SourcePath); err != nil {
		return model, err
	}
	if err := writeHostLink(link.DestinationPath, link.SourcePath); err != nil {
		return model, err
	}

	model.ID = types.StringValue(model.Destination.ValueString())
	model.SourcePath = types.StringValue(link.SourcePath)
	return model, nil
}

type hostLinkSpec struct {
	DestinationPath string
	SourcePath      string
}

func hostLinkSpecFromModel(model HostLinkResourceModel) (hostLinkSpec, error) {
	if model.Destination.IsNull() || model.Destination.IsUnknown() {
		return hostLinkSpec{}, fmt.Errorf("destination must be known")
	}
	if model.Source.IsNull() || model.Source.IsUnknown() {
		return hostLinkSpec{}, fmt.Errorf("source must be known")
	}

	destinationPath, err := resolveHostLinkDestination(model.Destination.ValueString())
	if err != nil {
		return hostLinkSpec{}, fmt.Errorf("invalid destination: %w", err)
	}
	sourcePath, err := resolveHostLinkSource(model.Source.ValueString())
	if err != nil {
		return hostLinkSpec{}, fmt.Errorf("invalid source: %w", err)
	}

	return hostLinkSpec{
		DestinationPath: destinationPath,
		SourcePath:      sourcePath,
	}, nil
}

func resolveHostLinkDestination(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("destination must be non-empty")
	}
	if strings.Contains(value, "\x00") {
		return "", fmt.Errorf("destination must not contain NUL bytes")
	}

	if strings.HasPrefix(value, "~") {
		return expandHostPath(value)
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

func resolveHostLinkSource(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("source must be non-empty")
	}
	if strings.Contains(value, "\x00") {
		return "", fmt.Errorf("source must not contain NUL bytes")
	}

	if strings.HasPrefix(value, "~") {
		return expandHostPath(value)
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
		if err := os.Remove(destinationPath); err != nil {
			return fmt.Errorf("remove stale symbolic link %q: %w", destinationPath, err)
		}
	}

	if err := os.Symlink(sourcePath, destinationPath); err != nil {
		return fmt.Errorf("create symbolic link %q -> %q: %w", destinationPath, sourcePath, err)
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
