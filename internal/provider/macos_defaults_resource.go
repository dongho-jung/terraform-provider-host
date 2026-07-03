package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource               = &MacOSDefaultsResource{}
	_ resource.ResourceWithConfigure  = &MacOSDefaultsResource{}
	_ resource.ResourceWithModifyPlan = &MacOSDefaultsResource{}
)

const macOSDefaultsResourceID = "macos_defaults"

type MacOSDefaultsResource struct {
	manager MacOSDefaultsManager
}

type MacOSDefaultsResourceModel struct {
	ID       types.String `tfsdk:"id"`
	Defaults types.Map    `tfsdk:"defaults"`
}

type MacOSDefaultsDefaultModel struct {
	Domain          types.String  `tfsdk:"domain"`
	Key             types.String  `tfsdk:"key"`
	CurrentHost     types.Bool    `tfsdk:"current_host"`
	Bool            types.Bool    `tfsdk:"bool"`
	Int             types.Int64   `tfsdk:"int"`
	Float           types.Float64 `tfsdk:"float"`
	String          types.String  `tfsdk:"string"`
	StringList      types.List    `tfsdk:"string_list"`
	DeleteOnDestroy types.Bool    `tfsdk:"delete_on_destroy"`
	Restart         types.List    `tfsdk:"restart"`
}

type macOSDefaultsNamedSpec struct {
	Name  string
	Model MacOSDefaultsDefaultModel
	Spec  macOSDefaultSpec
}

func NewMacOSDefaultsResource() resource.Resource {
	return &MacOSDefaultsResource{}
}

func (r *MacOSDefaultsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_macos_defaults"
}

func (r *MacOSDefaultsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages multiple macOS `defaults` keys as one grouped Terraform resource.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, always `macos_defaults`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"defaults": schema.MapNestedAttribute{
				Required:            true,
				MarkdownDescription: "Named macOS defaults to manage. Map keys are Terraform-local names; each object is identified on macOS by `domain`, `key`, and `current_host`.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"domain": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Defaults domain, such as `com.apple.dock`, `NSGlobalDomain`, or `com.apple.universalaccess`.",
						},
						"key": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Defaults key to manage inside the domain.",
						},
						"current_host": schema.BoolAttribute{
							Optional:            true,
							MarkdownDescription: "Use `defaults -currentHost` for host-specific preferences.",
						},
						"bool": schema.BoolAttribute{
							Optional:            true,
							MarkdownDescription: "Boolean value. Exactly one of `bool`, `int`, `float`, `string`, or `string_list` must be set.",
						},
						"int": schema.Int64Attribute{
							Optional:            true,
							MarkdownDescription: "Integer value. Exactly one of `bool`, `int`, `float`, `string`, or `string_list` must be set.",
						},
						"float": schema.Float64Attribute{
							Optional:            true,
							MarkdownDescription: "Floating-point value. Exactly one of `bool`, `int`, `float`, `string`, or `string_list` must be set.",
						},
						"string": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "String value. Exactly one of `bool`, `int`, `float`, `string`, or `string_list` must be set.",
						},
						"string_list": schema.ListAttribute{
							ElementType:         types.StringType,
							Optional:            true,
							MarkdownDescription: "String array value. Exactly one of `bool`, `int`, `float`, `string`, or `string_list` must be set.",
						},
						"delete_on_destroy": schema.BoolAttribute{
							Optional:            true,
							MarkdownDescription: "Delete this defaults key on destroy. Defaults to false, leaving the current host setting in place.",
						},
						"restart": schema.ListAttribute{
							ElementType:         types.StringType,
							Optional:            true,
							MarkdownDescription: "Process names to restart with `killall` after writes or deletes, such as `Dock`, `Finder`, or `SystemUIServer`. Omit to use provider defaults for known domains; set `[]` to disable restarts.",
						},
					},
				},
			},
		},
	}
}

func (r *MacOSDefaultsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.MacOSDefaultsManager == nil {
			resp.Diagnostics.AddError("macOS defaults unavailable", "`host_macos_defaults` requires the macOS `defaults` command.")
			return
		}
		r.manager = data.MacOSDefaultsManager
	case MacOSDefaultsManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or MacOSDefaultsManager, got %T.", req.ProviderData),
		)
	}
}

func (r *MacOSDefaultsResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan MacOSDefaultsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if macOSDefaultsPlanReady(plan) {
		_, diags := macOSDefaultsSpecsFromModel(ctx, plan)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	plan.ID = types.StringValue(macOSDefaultsResourceID)
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *MacOSDefaultsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MacOSDefaultsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncDefaults(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync macOS defaults", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSDefaultsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MacOSDefaultsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, err := r.readDefaults(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read macOS defaults", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *MacOSDefaultsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var state MacOSDefaultsResourceModel
	var plan MacOSDefaultsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, err := r.updateDefaults(ctx, state, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync macOS defaults", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *MacOSDefaultsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MacOSDefaultsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	specs, diags := macOSDefaultsSpecsFromModel(ctx, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if r.manager == nil {
		resp.Diagnostics.AddError("macOS defaults unavailable", "`host_macos_defaults` requires the macOS `defaults` command.")
		return
	}

	var restartProcesses []string
	for _, spec := range specs {
		if !spec.Spec.DeleteOnDestroy {
			continue
		}
		if err := r.manager.DeleteDefault(ctx, spec.Spec); err != nil {
			resp.Diagnostics.AddError("Failed to delete macOS default", err.Error())
			return
		}
		restartProcesses = append(restartProcesses, spec.Spec.Restart...)
	}

	if err := r.manager.RestartProcesses(ctx, uniqueStrings(restartProcesses)); err != nil {
		resp.Diagnostics.AddError("Failed to restart macOS processes", err.Error())
	}
}

func (r *MacOSDefaultsResource) syncDefaults(ctx context.Context, model MacOSDefaultsResourceModel) (MacOSDefaultsResourceModel, error) {
	if r.manager == nil {
		return model, fmt.Errorf("macOS defaults manager unavailable")
	}

	specs, diags := macOSDefaultsSpecsFromModel(ctx, model)
	if diags.HasError() {
		return model, diagnosticsError(diags)
	}

	var restartProcesses []string
	for _, spec := range specs {
		if err := r.manager.WriteDefault(ctx, spec.Spec); err != nil {
			return model, err
		}
		restartProcesses = append(restartProcesses, spec.Spec.Restart...)
	}
	if err := r.manager.RestartProcesses(ctx, uniqueStrings(restartProcesses)); err != nil {
		return model, err
	}

	return macOSDefaultsModelFromSpecs(ctx, specs)
}

func (r *MacOSDefaultsResource) updateDefaults(ctx context.Context, prior MacOSDefaultsResourceModel, plan MacOSDefaultsResourceModel) (MacOSDefaultsResourceModel, error) {
	if r.manager == nil {
		return plan, fmt.Errorf("macOS defaults manager unavailable")
	}

	priorSpecs, priorDiags := macOSDefaultsSpecsFromModel(ctx, prior)
	planSpecs, planDiags := macOSDefaultsSpecsFromModel(ctx, plan)
	priorDiags.Append(planDiags...)
	if priorDiags.HasError() {
		return plan, diagnosticsError(priorDiags)
	}

	plannedIDs := make(map[string]struct{}, len(planSpecs))
	for _, spec := range planSpecs {
		plannedIDs[spec.Spec.ID] = struct{}{}
	}

	var restartProcesses []string
	for _, spec := range priorSpecs {
		if _, stillManaged := plannedIDs[spec.Spec.ID]; stillManaged || !spec.Spec.DeleteOnDestroy {
			continue
		}
		if err := r.manager.DeleteDefault(ctx, spec.Spec); err != nil {
			return plan, err
		}
		restartProcesses = append(restartProcesses, spec.Spec.Restart...)
	}

	for _, spec := range planSpecs {
		if err := r.manager.WriteDefault(ctx, spec.Spec); err != nil {
			return plan, err
		}
		restartProcesses = append(restartProcesses, spec.Spec.Restart...)
	}
	if err := r.manager.RestartProcesses(ctx, uniqueStrings(restartProcesses)); err != nil {
		return plan, err
	}

	return macOSDefaultsModelFromSpecs(ctx, planSpecs)
}

func (r *MacOSDefaultsResource) readDefaults(ctx context.Context, model MacOSDefaultsResourceModel) (MacOSDefaultsResourceModel, error) {
	if r.manager == nil {
		return model, fmt.Errorf("macOS defaults manager unavailable")
	}

	specs, diags := macOSDefaultsSpecsFromModel(ctx, model)
	if diags.HasError() {
		return model, diagnosticsError(diags)
	}

	var existingSpecs []macOSDefaultsNamedSpec
	for _, spec := range specs {
		actual, exists, err := r.manager.ReadDefault(ctx, spec.Spec)
		if err != nil {
			return model, err
		}
		if !exists {
			continue
		}
		spec.Spec.Value = actual
		existingSpecs = append(existingSpecs, spec)
	}

	return macOSDefaultsModelFromSpecs(ctx, existingSpecs)
}

func macOSDefaultsPlanReady(model MacOSDefaultsResourceModel) bool {
	if model.Defaults.IsNull() || model.Defaults.IsUnknown() {
		return false
	}
	for _, value := range model.Defaults.Elements() {
		if value.IsUnknown() {
			return false
		}
		object, ok := value.(types.Object)
		if !ok {
			continue
		}
		for _, attrValue := range object.Attributes() {
			if attrValue.IsUnknown() {
				return false
			}
		}
	}
	return true
}

func macOSDefaultsSpecsFromModel(ctx context.Context, model MacOSDefaultsResourceModel) ([]macOSDefaultsNamedSpec, diag.Diagnostics) {
	var diags diag.Diagnostics
	if model.Defaults.IsNull() {
		diags.AddError("Invalid macOS defaults", "defaults must not be null")
		return nil, diags
	}
	if model.Defaults.IsUnknown() {
		diags.AddError("Invalid macOS defaults", "defaults must be known")
		return nil, diags
	}

	var defaults map[string]MacOSDefaultsDefaultModel
	diags.Append(model.Defaults.ElementsAs(ctx, &defaults, false)...)
	if diags.HasError() {
		return nil, diags
	}

	names := make([]string, 0, len(defaults))
	for name := range defaults {
		names = append(names, name)
	}
	sort.Strings(names)

	seenIDs := make(map[string]string, len(defaults))
	specs := make([]macOSDefaultsNamedSpec, 0, len(defaults))
	for _, name := range names {
		item := defaults[name]
		spec, specDiags := macOSDefaultSpecFromModel(ctx, MacOSDefaultResourceModel{
			Domain:          item.Domain,
			Key:             item.Key,
			CurrentHost:     item.CurrentHost,
			Bool:            item.Bool,
			Int:             item.Int,
			Float:           item.Float,
			String:          item.String,
			StringList:      item.StringList,
			DeleteOnDestroy: item.DeleteOnDestroy,
			Restart:         item.Restart,
		})
		diags.Append(specDiags...)
		if specDiags.HasError() {
			continue
		}

		if previousName, ok := seenIDs[spec.ID]; ok {
			diags.AddError(
				"Duplicate macOS default",
				fmt.Sprintf("defaults.%s and defaults.%s both manage %q.", previousName, name, spec.ID),
			)
			continue
		}
		seenIDs[spec.ID] = name
		specs = append(specs, macOSDefaultsNamedSpec{
			Name:  name,
			Model: item,
			Spec:  spec,
		})
	}
	return specs, diags
}

func macOSDefaultsModelFromSpecs(ctx context.Context, specs []macOSDefaultsNamedSpec) (MacOSDefaultsResourceModel, error) {
	var diags diag.Diagnostics
	elements := make(map[string]attr.Value, len(specs))
	for _, spec := range specs {
		model, err := macOSDefaultsDefaultModelWithValue(ctx, spec.Model, spec.Spec.Value)
		if err != nil {
			return MacOSDefaultsResourceModel{}, err
		}
		objectValue, objectDiags := types.ObjectValue(macOSDefaultsDefaultAttributeTypes(), map[string]attr.Value{
			"domain":            model.Domain,
			"key":               model.Key,
			"current_host":      model.CurrentHost,
			"bool":              model.Bool,
			"int":               model.Int,
			"float":             model.Float,
			"string":            model.String,
			"string_list":       model.StringList,
			"delete_on_destroy": model.DeleteOnDestroy,
			"restart":           model.Restart,
		})
		diags.Append(objectDiags...)
		if diags.HasError() {
			return MacOSDefaultsResourceModel{}, diagnosticsError(diags)
		}
		elements[spec.Name] = objectValue
	}

	defaults, mapDiags := types.MapValue(macOSDefaultsDefaultObjectType(), elements)
	diags.Append(mapDiags...)
	if diags.HasError() {
		return MacOSDefaultsResourceModel{}, diagnosticsError(diags)
	}

	return MacOSDefaultsResourceModel{
		ID:       types.StringValue(macOSDefaultsResourceID),
		Defaults: defaults,
	}, nil
}

func macOSDefaultsDefaultModelWithValue(ctx context.Context, model MacOSDefaultsDefaultModel, value macOSDefaultValue) (MacOSDefaultsDefaultModel, error) {
	resourceModel := MacOSDefaultResourceModel{
		Domain:          model.Domain,
		Key:             model.Key,
		CurrentHost:     model.CurrentHost,
		DeleteOnDestroy: model.DeleteOnDestroy,
		Restart:         model.Restart,
	}
	resourceModel, err := macOSDefaultModelWithValue(ctx, resourceModel, value)
	if err != nil {
		return model, err
	}

	model.Bool = resourceModel.Bool
	model.Int = resourceModel.Int
	model.Float = resourceModel.Float
	model.String = resourceModel.String
	model.StringList = resourceModel.StringList
	return model, nil
}

func macOSDefaultsDefaultAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"domain":            types.StringType,
		"key":               types.StringType,
		"current_host":      types.BoolType,
		"bool":              types.BoolType,
		"int":               types.Int64Type,
		"float":             types.Float64Type,
		"string":            types.StringType,
		"string_list":       types.ListType{ElemType: types.StringType},
		"delete_on_destroy": types.BoolType,
		"restart":           types.ListType{ElemType: types.StringType},
	}
}

func macOSDefaultsDefaultObjectType() types.ObjectType {
	return types.ObjectType{
		AttrTypes: macOSDefaultsDefaultAttributeTypes(),
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var result []string
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
