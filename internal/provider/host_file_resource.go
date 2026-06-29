package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	hostFileRenderMarkers = "markers"
	hostFileRenderClean   = "clean"
)

var (
	_ resource.Resource               = &HostFileResource{}
	_ resource.ResourceWithModifyPlan = &HostFileResource{}
)

type HostFileResource struct {
}

type HostFileResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Path    types.String `tfsdk:"path"`
	Render  types.String `tfsdk:"render"`
	Content types.String `tfsdk:"content"`
	Block   types.Map    `tfsdk:"block"`
}

type HostFileBlockReferenceModel struct {
	Name   types.String `tfsdk:"name"`
	Path   types.String `tfsdk:"path"`
	Render types.String `tfsdk:"render"`
}

type HostFileBlockSectionModel struct {
	Name     types.String `tfsdk:"name"`
	Path     types.String `tfsdk:"path"`
	Render   types.String `tfsdk:"render"`
	Priority types.Int64  `tfsdk:"priority"`
	Content  types.String `tfsdk:"content"`
}

type hostFileBlockSpec struct {
	Name     string
	Priority int64
	Content  *string
}

func NewHostFileResource() resource.Resource {
	return &HostFileResource{}
}

func (r *HostFileResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file"
}

func (r *HostFileResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages whole host files or named Terraform-owned sections inside host files.",
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
				MarkdownDescription: "Path to the host file. `~` is expanded to the current user's home directory when the provider reads or writes the file.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"render": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(hostFileRenderMarkers),
				MarkdownDescription: "How block mode is rendered. `markers` writes Terraform marker comments into the file. `clean` renders a marker-free file and stores block tracking metadata under `~/.terraform-provider-host`.",
			},
			"content": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Full file content to manage without Terraform section markers. Mutually exclusive with `block`.",
			},
			"block": schema.MapNestedAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Named file sections. Use map keys as section names so they can be referenced with `block[\"name\"]`.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "File section name.",
						},
						"path": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Path to the host file that contains this section.",
						},
						"render": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Render mode inherited from the containing host file.",
						},
						"priority": schema.Int64Attribute{
							Optional:            true,
							Computed:            true,
							Default:             int64default.StaticInt64(0),
							MarkdownDescription: "Sort priority for this section. Sections are ordered by `priority`, then section name.",
						},
						"content": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Content managed directly by the host file section. `host_file_block` resources targeting this section are rendered after this content.",
						},
					},
				},
			},
		},
	}
}

func (r *HostFileResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostFileResourceModel
	var config HostFileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.Path.IsNull() || plan.Path.IsUnknown() {
		return
	}
	if _, err := expandHostPath(plan.Path.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid host file path", err.Error())
		return
	}
	if err := validateHostFileRender(hostFileRender(plan)); err != nil {
		resp.Diagnostics.AddError("Invalid host file render mode", err.Error())
		return
	}
	if hostFileUsesFullContent(config) {
		if hostFileHasConfiguredBlocks(config.Block) {
			resp.Diagnostics.AddError("Invalid host file configuration", "`content` and `block` are mutually exclusive.")
			return
		}

		plan.ID = types.StringValue(plan.Path.ValueString())
		plan.Render = types.StringValue(hostFileRender(plan))
		plan.Block = types.MapNull(hostFileBlockReferenceObjectType())
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
		return
	}

	blockSpecs, blockDiags := hostFileBlockSpecs(ctx, config.Block)
	resp.Diagnostics.Append(blockDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateHostFileBlockSpecs(blockSpecs); err != nil {
		resp.Diagnostics.AddError("Invalid host file block name", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.Path.ValueString())
	plan.Render = types.StringValue(hostFileRender(plan))
	plan.Block = hostFileBlockMapValue(plan.Path.ValueString(), hostFileRender(plan), blockSpecs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostFileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostFileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if hostFileUsesFullContent(plan) {
		if err := withLockedHostFile(ctx, plan.Path.ValueString(), func(path string) error {
			return syncHostFileContent(path, plan.Content.ValueString())
		}); err != nil {
			resp.Diagnostics.AddError("Failed to sync host file", err.Error())
			return
		}

		plan.ID = types.StringValue(plan.Path.ValueString())
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	blockSpecs, blockDiags := hostFileBlockSpecs(ctx, plan.Block)
	resp.Diagnostics.Append(blockDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := syncHostFileByRender(ctx, plan.Path.ValueString(), hostFileRender(plan), blockSpecs); err != nil {
		resp.Diagnostics.AddError("Failed to sync host file", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.Path.ValueString())
	plan.Render = types.StringValue(hostFileRender(plan))
	plan.Block = hostFileBlockMapValue(plan.Path.ValueString(), hostFileRender(plan), blockSpecs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *HostFileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostFileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if hostFileUsesFullContent(state) {
		var exists bool
		var content string
		if err := withLockedHostFile(ctx, state.Path.ValueString(), func(path string) error {
			var err error
			content, exists, err = readHostFileContent(path)
			return err
		}); err != nil {
			resp.Diagnostics.AddError("Failed to read host file", err.Error())
			return
		}
		if !exists {
			resp.State.RemoveResource(ctx)
			return
		}
		if content != canonicalHostFileContent(state.Content.ValueString()) {
			state.Content = types.StringValue(trimRenderedManagedBlockBody(content))
		}
		state.ID = types.StringValue(state.Path.ValueString())
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}

	blockSpecs, blockDiags := hostFileBlockSpecs(ctx, state.Block)
	resp.Diagnostics.Append(blockDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var exists bool
	if err := withLockedHostFile(ctx, state.Path.ValueString(), func(path string) error {
		var err error
		if hostFileRender(state) == hostFileRenderClean {
			blockSpecs, exists, err = readCleanHostFileBlockSpecs(path, blockSpecs)
		} else {
			blockSpecs, exists, err = readHostFileBlockSpecs(path, blockSpecs)
		}
		return err
	}); err != nil {
		resp.Diagnostics.AddError("Failed to read host file", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(state.Path.ValueString())
	state.Render = types.StringValue(hostFileRender(state))
	state.Block = hostFileBlockMapValue(state.Path.ValueString(), hostFileRender(state), blockSpecs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostFileResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostFileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if hostFileUsesFullContent(plan) {
		if err := withLockedHostFile(ctx, plan.Path.ValueString(), func(path string) error {
			return syncHostFileContent(path, plan.Content.ValueString())
		}); err != nil {
			resp.Diagnostics.AddError("Failed to sync host file", err.Error())
			return
		}

		plan.ID = types.StringValue(plan.Path.ValueString())
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	blockSpecs, blockDiags := hostFileBlockSpecs(ctx, plan.Block)
	resp.Diagnostics.Append(blockDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := syncHostFileByRender(ctx, plan.Path.ValueString(), hostFileRender(plan), blockSpecs); err != nil {
		resp.Diagnostics.AddError("Failed to sync host file", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.Path.ValueString())
	plan.Render = types.StringValue(hostFileRender(plan))
	plan.Block = hostFileBlockMapValue(plan.Path.ValueString(), hostFileRender(plan), blockSpecs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *HostFileResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostFileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if hostFileUsesFullContent(state) {
		if err := withLockedHostFile(ctx, state.Path.ValueString(), func(path string) error {
			return deleteHostFile(path)
		}); err != nil {
			resp.Diagnostics.AddError("Failed to delete host file", err.Error())
		}
		return
	}

	blockSpecs, blockDiags := hostFileBlockSpecs(ctx, state.Block)
	resp.Diagnostics.Append(blockDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := deleteHostFileByRender(ctx, state.Path.ValueString(), hostFileRender(state), blockSpecs); err != nil {
		resp.Diagnostics.AddError("Failed to delete host file blocks", err.Error())
	}
}

func hostFileBlockSpecs(ctx context.Context, blocks types.Map) ([]hostFileBlockSpec, diag.Diagnostics) {
	var diags diag.Diagnostics
	if blocks.IsNull() {
		return nil, diags
	}
	if blocks.IsUnknown() {
		diags.AddError("Invalid host file block map", "block map is unknown")
		return nil, diags
	}

	var elements map[string]HostFileBlockSectionModel
	diags.Append(blocks.ElementsAs(ctx, &elements, false)...)
	if diags.HasError() {
		return nil, diags
	}

	specs := make([]hostFileBlockSpec, 0, len(elements))
	for name, element := range elements {
		spec := hostFileBlockSpec{Name: name}
		if !element.Priority.IsNull() && !element.Priority.IsUnknown() {
			spec.Priority = element.Priority.ValueInt64()
		}
		if !element.Content.IsNull() && !element.Content.IsUnknown() {
			content := element.Content.ValueString()
			spec.Content = &content
		}
		specs = append(specs, spec)
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].Name < specs[j].Name
	})

	return specs, diags
}

func hostFileBlockSpecNames(specs []hostFileBlockSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}

	return sortedHostFileBlockNames(names)
}

func validateHostFileBlockSpecs(specs []hostFileBlockSpec) error {
	return validateHostFileBlockNames(hostFileBlockSpecNames(specs))
}

func hostFileBlockMapValue(path string, render string, specs []hostFileBlockSpec, diags *diag.Diagnostics) types.Map {
	elements := make(map[string]attr.Value, len(specs))
	for _, spec := range sortedHostFileBlockSpecs(specs) {
		contentValue := types.StringNull()
		if spec.Content != nil {
			contentValue = types.StringValue(*spec.Content)
		}

		objectValue, objectDiags := types.ObjectValue(
			hostFileBlockReferenceAttributeTypes(),
			map[string]attr.Value{
				"name":     types.StringValue(spec.Name),
				"path":     types.StringValue(path),
				"render":   types.StringValue(render),
				"priority": types.Int64Value(spec.Priority),
				"content":  contentValue,
			},
		)
		diags.Append(objectDiags...)
		if diags.HasError() {
			return types.MapUnknown(hostFileBlockReferenceObjectType())
		}

		elements[spec.Name] = objectValue
	}

	mapValue, mapDiags := types.MapValue(hostFileBlockReferenceObjectType(), elements)
	diags.Append(mapDiags...)
	if diags.HasError() {
		return types.MapUnknown(hostFileBlockReferenceObjectType())
	}

	return mapValue
}

func hostFileBlockReferenceAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"name":     types.StringType,
		"path":     types.StringType,
		"render":   types.StringType,
		"priority": types.Int64Type,
		"content":  types.StringType,
	}
}

func hostFileBlockReferenceObjectType() types.ObjectType {
	return types.ObjectType{
		AttrTypes: hostFileBlockReferenceAttributeTypes(),
	}
}

func sortedHostFileBlockSpecs(specs []hostFileBlockSpec) []hostFileBlockSpec {
	sortedSpecs := append([]hostFileBlockSpec(nil), specs...)
	sort.Slice(sortedSpecs, func(i, j int) bool {
		if sortedSpecs[i].Priority != sortedSpecs[j].Priority {
			return sortedSpecs[i].Priority < sortedSpecs[j].Priority
		}

		return sortedSpecs[i].Name < sortedSpecs[j].Name
	})

	return sortedSpecs
}

func hostFileUsesFullContent(model HostFileResourceModel) bool {
	return !model.Content.IsNull() && !model.Content.IsUnknown()
}

func hostFileHasConfiguredBlocks(blocks types.Map) bool {
	return !blocks.IsNull() && !blocks.IsUnknown() && len(blocks.Elements()) > 0
}

func hostFileRender(model HostFileResourceModel) string {
	if model.Render.IsNull() || model.Render.IsUnknown() || model.Render.ValueString() == "" {
		return hostFileRenderMarkers
	}

	return model.Render.ValueString()
}

func validateHostFileRender(render string) error {
	switch render {
	case hostFileRenderMarkers, hostFileRenderClean:
		return nil
	default:
		return fmt.Errorf("render must be %q or %q", hostFileRenderMarkers, hostFileRenderClean)
	}
}

func syncHostFileByRender(ctx context.Context, path string, render string, blockSpecs []hostFileBlockSpec) error {
	return withLockedHostFile(ctx, path, func(resolvedPath string) error {
		if render == hostFileRenderClean {
			return syncCleanHostFileBlocks(resolvedPath, blockSpecs)
		}

		return syncHostFileBlocks(resolvedPath, blockSpecs)
	})
}

func deleteHostFileByRender(ctx context.Context, path string, render string, blockSpecs []hostFileBlockSpec) error {
	return withLockedHostFile(ctx, path, func(resolvedPath string) error {
		if render == hostFileRenderClean {
			return deleteCleanHostFile(resolvedPath)
		}

		return deleteHostFileBlocks(resolvedPath, hostFileBlockSpecNames(blockSpecs))
	})
}
