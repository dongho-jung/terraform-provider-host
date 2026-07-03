package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource               = &HostFileResource{}
	_ resource.ResourceWithModifyPlan = &HostFileResource{}
)

type HostFileResource struct {
}

type HostFileResourceModel struct {
	ID              types.String `tfsdk:"id"`
	Path            types.String `tfsdk:"path"`
	PathResolved    types.String `tfsdk:"path_resolved"`
	Content         types.String `tfsdk:"content"`
	RenderedContent types.String `tfsdk:"rendered_content"`
	Block           types.List   `tfsdk:"block"`
	Blocks          types.Map    `tfsdk:"blocks"`
}

type HostFileBlockReferenceModel struct {
	Name         types.String `tfsdk:"name"`
	Path         types.String `tfsdk:"path"`
	PathResolved types.String `tfsdk:"path_resolved"`
}

type HostFileBlockModel struct {
	Name    types.String `tfsdk:"name"`
	Before  types.List   `tfsdk:"before"`
	After   types.List   `tfsdk:"after"`
	Content types.String `tfsdk:"content"`
}

type hostFileBlockSpec struct {
	Name    string
	Order   int
	Before  []string
	After   []string
	Content *string
}

func NewHostFileResource() resource.Resource {
	return &HostFileResource{}
}

func (r *HostFileResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file"
}

func (r *HostFileResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             3,
		MarkdownDescription: "Manages whole host files or named Terraform-owned blocks inside host files.",
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
			"path_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute host file path.",
			},
			"content": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Full file content to manage without Terraform block markers. Mutually exclusive with `block`.",
			},
			"rendered_content": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Last content observed in the host file. Used to detect drift.",
			},
			"blocks": schema.MapNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Computed file block references for `host_file_block` resources.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "File block name.",
						},
						"path": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Path to the host file that contains this block.",
						},
						"path_resolved": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Resolved absolute host file path that contains this block.",
						},
					},
				},
			},
		},
		Blocks: map[string]schema.Block{
			"block": schema.ListNestedBlock{
				MarkdownDescription: "Named file block. Declaration order controls render order unless `before` or `after` is set.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "File block name.",
						},
						"before": schema.ListAttribute{
							ElementType:         types.StringType,
							Optional:            true,
							MarkdownDescription: "Names of sibling file blocks that this block must be rendered before.",
						},
						"after": schema.ListAttribute{
							ElementType:         types.StringType,
							Optional:            true,
							MarkdownDescription: "Names of sibling file blocks that this block must be rendered after.",
						},
						"content": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Content managed directly by the host file block. `host_file_block` resources targeting the same block are rendered after this content.",
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
	resolvedPath, err := expandHostPath(plan.Path.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid host file path", err.Error())
		return
	}
	plan.PathResolved = types.StringValue(resolvedPath)
	if hostFileUsesFullContent(config) {
		if hostFileHasConfiguredBlocks(config.Block) {
			resp.Diagnostics.AddError("Invalid host file configuration", "`content` is mutually exclusive with `block`.")
			return
		}

		plan.ID = types.StringValue(plan.Path.ValueString())
		plan.Blocks = types.MapNull(hostFileBlockReferenceObjectType())
		plan.RenderedContent = types.StringValue(canonicalHostFileContent(plan.Content.ValueString()))
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
	plan.Blocks = hostFileBlockReferenceMapValue(plan.Path.ValueString(), resolvedPath, blockSpecs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	rendered, err := plannedCleanHostFileContent(plan.Path.ValueString(), blockSpecs)
	if err != nil {
		resp.Diagnostics.AddError("Failed to render host file plan", err.Error())
		return
	}
	plan.RenderedContent = types.StringValue(rendered)

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
		plan.PathResolved = hostFilePathResolvedValue(plan.Path.ValueString(), &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.Blocks = types.MapNull(hostFileBlockReferenceObjectType())
		plan.RenderedContent = types.StringValue(canonicalHostFileContent(plan.Content.ValueString()))
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	blockSpecs, blockDiags := hostFileBlockSpecs(ctx, plan.Block)
	resp.Diagnostics.Append(blockDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := withLockedHostFile(ctx, plan.Path.ValueString(), func(path string) error {
		return syncCleanHostFileBlocks(path, blockSpecs)
	}); err != nil {
		resp.Diagnostics.AddError("Failed to sync host file", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.Path.ValueString())
	resolvedPath := hostFilePathResolvedValue(plan.Path.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.PathResolved = resolvedPath
	plan.Blocks = hostFileBlockReferenceMapValue(plan.Path.ValueString(), resolvedPath.ValueString(), blockSpecs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	rendered, err := readRenderedHostFileContent(plan.Path.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read rendered host file", err.Error())
		return
	}
	plan.RenderedContent = types.StringValue(rendered)

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
		state.PathResolved = hostFilePathResolvedValue(state.Path.ValueString(), &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		state.Blocks = types.MapNull(hostFileBlockReferenceObjectType())
		state.RenderedContent = types.StringValue(content)
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
		blockSpecs, exists, err = readCleanHostFileBlockSpecs(path, blockSpecs)
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
	resolvedPath := hostFilePathResolvedValue(state.Path.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	state.PathResolved = resolvedPath
	state.Blocks = hostFileBlockReferenceMapValue(state.Path.ValueString(), resolvedPath.ValueString(), blockSpecs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	rendered, err := readRenderedHostFileContent(state.Path.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read rendered host file", err.Error())
		return
	}
	state.RenderedContent = types.StringValue(rendered)

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
		plan.PathResolved = hostFilePathResolvedValue(plan.Path.ValueString(), &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.Blocks = types.MapNull(hostFileBlockReferenceObjectType())
		plan.RenderedContent = types.StringValue(canonicalHostFileContent(plan.Content.ValueString()))
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	blockSpecs, blockDiags := hostFileBlockSpecs(ctx, plan.Block)
	resp.Diagnostics.Append(blockDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := withLockedHostFile(ctx, plan.Path.ValueString(), func(path string) error {
		return syncCleanHostFileBlocks(path, blockSpecs)
	}); err != nil {
		resp.Diagnostics.AddError("Failed to sync host file", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.Path.ValueString())
	resolvedPath := hostFilePathResolvedValue(plan.Path.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.PathResolved = resolvedPath
	plan.Blocks = hostFileBlockReferenceMapValue(plan.Path.ValueString(), resolvedPath.ValueString(), blockSpecs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	rendered, err := readRenderedHostFileContent(plan.Path.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read rendered host file", err.Error())
		return
	}
	plan.RenderedContent = types.StringValue(rendered)

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

	if err := withLockedHostFile(ctx, state.Path.ValueString(), func(path string) error {
		return deleteCleanHostFile(path)
	}); err != nil {
		resp.Diagnostics.AddError("Failed to delete host file blocks", err.Error())
	}
}

func hostFileBlockSpecs(ctx context.Context, blocks types.List) ([]hostFileBlockSpec, diag.Diagnostics) {
	var diags diag.Diagnostics
	if blocks.IsNull() {
		return nil, diags
	}
	if blocks.IsUnknown() {
		diags.AddError("Invalid host file block list", "block list is unknown")
		return nil, diags
	}

	var elements []HostFileBlockModel
	diags.Append(blocks.ElementsAs(ctx, &elements, false)...)
	if diags.HasError() {
		return nil, diags
	}

	specs := make([]hostFileBlockSpec, 0, len(elements))
	for i, element := range elements {
		if element.Name.IsNull() || element.Name.IsUnknown() {
			diags.AddError("Invalid host file block", "block name must be known")
			return nil, diags
		}

		before, beforeDiags := stringListValue(ctx, element.Before, "host file block before")
		diags.Append(beforeDiags...)
		after, afterDiags := stringListValue(ctx, element.After, "host file block after")
		diags.Append(afterDiags...)
		if diags.HasError() {
			return nil, diags
		}

		spec := hostFileBlockSpec{
			Name:   element.Name.ValueString(),
			Order:  i,
			Before: before,
			After:  after,
		}
		if !element.Content.IsNull() && !element.Content.IsUnknown() {
			content := strings.TrimSpace(element.Content.ValueString())
			spec.Content = &content
		}
		specs = append(specs, spec)
	}

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
	if err := validateHostFileBlockNames(hostFileBlockSpecNames(specs)); err != nil {
		return err
	}

	_, err := sortHostFileBlockSpecs(specs)
	return err
}

func sortHostFileBlockSpecs(specs []hostFileBlockSpec) ([]hostFileBlockSpec, error) {
	byName := make(map[string]hostFileBlockSpec, len(specs))
	for _, spec := range specs {
		byName[spec.Name] = spec
	}

	outgoing := make(map[string][]string, len(specs))
	indegree := make(map[string]int, len(specs))
	for _, spec := range specs {
		indegree[spec.Name] = 0
	}

	addEdge := func(from string, to string) error {
		if from == to {
			return fmt.Errorf("host file block %q cannot order itself", from)
		}
		if _, ok := byName[from]; !ok {
			return fmt.Errorf("host file block %q references unknown block %q", to, from)
		}
		if _, ok := byName[to]; !ok {
			return fmt.Errorf("host file block %q references unknown block %q", from, to)
		}
		for _, existing := range outgoing[from] {
			if existing == to {
				return nil
			}
		}
		outgoing[from] = append(outgoing[from], to)
		indegree[to]++

		return nil
	}

	for _, spec := range specs {
		for _, after := range spec.After {
			if err := addEdge(after, spec.Name); err != nil {
				return nil, err
			}
		}
		for _, before := range spec.Before {
			if err := addEdge(spec.Name, before); err != nil {
				return nil, err
			}
		}
	}

	remaining := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		remaining[spec.Name] = struct{}{}
	}

	sortedSpecs := make([]hostFileBlockSpec, 0, len(specs))
	for len(remaining) > 0 {
		candidates := make([]hostFileBlockSpec, 0, len(remaining))
		for name := range remaining {
			if indegree[name] == 0 {
				candidates = append(candidates, byName[name])
			}
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("host file block ordering contains a cycle")
		}
		sort.Slice(candidates, func(i int, j int) bool {
			return hostFileBlockSpecLess(candidates[i], candidates[j])
		})

		next := candidates[0]
		sortedSpecs = append(sortedSpecs, next)
		delete(remaining, next.Name)
		for _, to := range outgoing[next.Name] {
			indegree[to]--
		}
	}

	return sortedSpecs, nil
}

func hostFileBlockSpecLess(a hostFileBlockSpec, b hostFileBlockSpec) bool {
	if a.Order != b.Order {
		return a.Order < b.Order
	}

	return a.Name < b.Name
}

func hostFileBlockListValue(ctx context.Context, specs []hostFileBlockSpec, diags *diag.Diagnostics) types.List {
	elements := make([]HostFileBlockModel, 0, len(specs))
	for _, spec := range specs {
		before := types.ListNull(types.StringType)
		if len(spec.Before) > 0 {
			beforeValue, beforeDiags := types.ListValueFrom(ctx, types.StringType, spec.Before)
			diags.Append(beforeDiags...)
			before = beforeValue
		}
		after := types.ListNull(types.StringType)
		if len(spec.After) > 0 {
			afterValue, afterDiags := types.ListValueFrom(ctx, types.StringType, spec.After)
			diags.Append(afterDiags...)
			after = afterValue
		}
		if diags.HasError() {
			return types.ListUnknown(hostFileBlockObjectType())
		}

		content := types.StringNull()
		if spec.Content != nil {
			content = types.StringValue(*spec.Content)
		}

		elements = append(elements, HostFileBlockModel{
			Name:    types.StringValue(spec.Name),
			Before:  before,
			After:   after,
			Content: content,
		})
	}

	block, blockDiags := types.ListValueFrom(ctx, hostFileBlockObjectType(), elements)
	diags.Append(blockDiags...)
	if diags.HasError() {
		return types.ListUnknown(hostFileBlockObjectType())
	}

	return block
}

func hostFileBlockObjectType() types.ObjectType {
	return types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"name":    types.StringType,
			"before":  types.ListType{ElemType: types.StringType},
			"after":   types.ListType{ElemType: types.StringType},
			"content": types.StringType,
		},
	}
}

func hostFileBlockReferenceMapValue(path string, pathResolved string, specs []hostFileBlockSpec, diags *diag.Diagnostics) types.Map {
	elements := make(map[string]attr.Value, len(specs))
	for _, spec := range sortedHostFileBlockSpecs(specs) {
		objectValue, objectDiags := types.ObjectValue(
			hostFileBlockReferenceAttributeTypes(),
			map[string]attr.Value{
				"name":          types.StringValue(spec.Name),
				"path":          types.StringValue(path),
				"path_resolved": types.StringValue(pathResolved),
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
		"name":          types.StringType,
		"path":          types.StringType,
		"path_resolved": types.StringType,
	}
}

func hostFileBlockReferenceObjectType() types.ObjectType {
	return types.ObjectType{
		AttrTypes: hostFileBlockReferenceAttributeTypes(),
	}
}

func sortedHostFileBlockSpecs(specs []hostFileBlockSpec) []hostFileBlockSpec {
	sortedSpecs, err := sortHostFileBlockSpecs(specs)
	if err != nil {
		return append([]hostFileBlockSpec(nil), specs...)
	}

	return sortedSpecs
}

func hostFileUsesFullContent(model HostFileResourceModel) bool {
	return !model.Content.IsNull() && !model.Content.IsUnknown()
}

func hostFileHasConfiguredBlocks(blocks types.List) bool {
	return !blocks.IsNull() && !blocks.IsUnknown() && len(blocks.Elements()) > 0
}

func hostFilePathResolvedValue(path string, diags *diag.Diagnostics) types.String {
	resolved, err := expandHostPath(path)
	if err != nil {
		diags.AddError("Invalid host file path", err.Error())
		return types.StringUnknown()
	}
	return types.StringValue(resolved)
}

func stringListValue(ctx context.Context, value types.List, label string) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if value.IsNull() {
		return nil, diags
	}
	if value.IsUnknown() {
		diags.AddError("Invalid "+label, label+" list is unknown")
		return nil, diags
	}

	var elements []string
	diags.Append(value.ElementsAs(ctx, &elements, false)...)
	if diags.HasError() {
		return nil, diags
	}

	return elements, diags
}
