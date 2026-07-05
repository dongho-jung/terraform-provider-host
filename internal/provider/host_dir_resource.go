package provider

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	tfpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &HostDirResource{}
	_ resource.ResourceWithConfigure   = &HostDirResource{}
	_ resource.ResourceWithImportState = &HostDirResource{}
	_ resource.ResourceWithModifyPlan  = &HostDirResource{}
)

type HostDirResource struct {
	homeDir string
}

type HostDirResourceModel struct {
	ID              types.String `tfsdk:"id"`
	Path            types.String `tfsdk:"path"`
	PathResolved    types.String `tfsdk:"path_resolved"`
	Mode            types.String `tfsdk:"mode"`
	RecursiveDelete types.Bool   `tfsdk:"recursive_delete"`
}

func NewHostDirResource() resource.Resource {
	return &HostDirResource{}
}

func (r *HostDirResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dir"
}

func (r *HostDirResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(HostProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected HostProviderData, got %T.", req.ProviderData))
		return
	}
	r.homeDir = data.HomeDir
}

func (r *HostDirResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a host directory.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, equal to `path`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Directory path. `~` is expanded to the provider `home_dir` and relative paths are resolved from the Terraform working directory.",
			},
			"path_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute directory path.",
			},
			"mode": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("0755"),
				MarkdownDescription: "Directory permission mode as four octal digits, such as `0755`.",
			},
			"recursive_delete": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Remove the directory tree recursively on destroy. Defaults to false, which only removes an empty directory.",
			},
		},
	}
}

func (r *HostDirResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostDirResourceModel
	var state HostDirResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	}
	if resp.Diagnostics.HasError() || plan.Path.IsNull() || plan.Path.IsUnknown() || plan.Mode.IsNull() || plan.Mode.IsUnknown() {
		return
	}

	resolvedPath, err := resolveHostDirPathForHome(plan.Path.ValueString(), r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Invalid host directory path", err.Error())
		return
	}
	if _, err := parseHostDirMode(plan.Mode.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid host directory mode", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.Path.ValueString())
	plan.PathResolved = types.StringValue(resolvedPath)
	requireReplaceIfResolvedPathChanged(req, resp, tfpath.Root("path"), state.Path, state.PathResolved, resolvedPath, func(value string) (string, error) {
		return resolveHostDirPathForHome(value, r.homeDir)
	})
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostDirResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostDirResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := syncHostDirForHome(plan, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync host directory", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostDirResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostDirResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, exists, err := readHostDirForHome(state, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read host directory", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostDirResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostDirResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := syncHostDirForHome(plan, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync host directory", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostDirResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostDirResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := deleteHostDirForHome(state, r.homeDir); err != nil {
		resp.Diagnostics.AddError("Failed to delete host directory", err.Error())
	}
}

func (r *HostDirResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, tfpath.Root("path"), req, resp)
}

func syncHostDir(model HostDirResourceModel) (HostDirResourceModel, error) {
	return syncHostDirForHome(model, "")
}

func syncHostDirForHome(model HostDirResourceModel, homeDir string) (HostDirResourceModel, error) {
	resolvedPath, err := resolveHostDirPathForHome(model.Path.ValueString(), homeDir)
	if err != nil {
		return model, err
	}
	mode, err := parseHostDirMode(model.Mode.ValueString())
	if err != nil {
		return model, err
	}

	if err := os.MkdirAll(resolvedPath, mode); err != nil {
		return model, fmt.Errorf("create directory %q: %w", resolvedPath, err)
	}
	if err := os.Chmod(resolvedPath, mode); err != nil {
		return model, fmt.Errorf("chmod directory %q: %w", resolvedPath, err)
	}

	return readExistingHostDir(model, resolvedPath)
}

func readHostDirForHome(model HostDirResourceModel, homeDir string) (HostDirResourceModel, bool, error) {
	resolvedPath, err := resolveHostDirPathForHome(model.Path.ValueString(), homeDir)
	if err != nil {
		return model, false, err
	}

	state, err := readExistingHostDir(model, resolvedPath)
	if os.IsNotExist(err) {
		return model, false, nil
	}
	if err != nil {
		return model, false, err
	}

	return state, true, nil
}

func readExistingHostDir(model HostDirResourceModel, resolvedPath string) (HostDirResourceModel, error) {
	info, err := os.Lstat(resolvedPath)
	if err != nil {
		return model, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return model, fmt.Errorf("path %q is a symbolic link, not a directory", resolvedPath)
	}
	if !info.IsDir() {
		return model, fmt.Errorf("path %q exists and is not a directory", resolvedPath)
	}

	model.ID = types.StringValue(model.Path.ValueString())
	model.PathResolved = types.StringValue(resolvedPath)
	model.Mode = types.StringValue(formatHostDirMode(info.Mode().Perm()))
	if model.RecursiveDelete.IsNull() || model.RecursiveDelete.IsUnknown() {
		model.RecursiveDelete = types.BoolValue(false)
	}
	return model, nil
}

func deleteHostDir(model HostDirResourceModel) error {
	return deleteHostDirForHome(model, "")
}

func deleteHostDirForHome(model HostDirResourceModel, homeDir string) error {
	resolvedPath, err := resolveHostDirPathForHome(model.Path.ValueString(), homeDir)
	if err != nil {
		return err
	}

	info, err := os.Lstat(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read directory %q: %w", resolvedPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path %q is a symbolic link; refusing to remove it as a host_dir", resolvedPath)
	}
	if !info.IsDir() {
		return fmt.Errorf("path %q exists and is not a directory", resolvedPath)
	}

	if model.RecursiveDelete.ValueBool() {
		return os.RemoveAll(resolvedPath)
	}
	if err := os.Remove(resolvedPath); err != nil {
		return fmt.Errorf("remove directory %q: %w. Set recursive_delete = true to remove a non-empty directory tree", resolvedPath, err)
	}
	return nil
}

func resolveHostDirPathForHome(value string, homeDir string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("path must be non-empty")
	}
	if strings.Contains(value, "\x00") {
		return "", fmt.Errorf("path must not contain NUL bytes")
	}
	return expandHostPathWithHome(value, homeDir)
}

func parseHostDirMode(value string) (os.FileMode, error) {
	value = strings.TrimSpace(value)
	if len(value) != 4 {
		return 0, fmt.Errorf("mode must be four octal digits, such as 0755")
	}
	parsed, err := strconv.ParseUint(value, 8, 32)
	if err != nil || parsed > 0o777 {
		return 0, fmt.Errorf("mode must be four octal digits between 0000 and 0777")
	}
	return os.FileMode(parsed), nil
}

func formatHostDirMode(mode os.FileMode) string {
	return fmt.Sprintf("%04o", uint32(mode)&0o777)
}
