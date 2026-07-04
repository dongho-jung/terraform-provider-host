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
	_ resource.Resource                = &MacOSDefaultsResource{}
	_ resource.ResourceWithConfigure   = &MacOSDefaultsResource{}
	_ resource.ResourceWithImportState = &MacOSDefaultsResource{}
	_ resource.ResourceWithModifyPlan  = &MacOSDefaultsResource{}
)

const macOSSettingsResourceID = "mac_settings"

type MacOSDefaultsResource struct {
	manager MacOSDefaultsManager
}

type MacOSDefaultsResourceModel struct {
	ID       types.String  `tfsdk:"id"`
	Settings types.Dynamic `tfsdk:"settings"`
	Groups   types.Dynamic `tfsdk:"groups"`
}

type MacOSDefaultsDefaultModel struct {
	Domain          types.String  `tfsdk:"domain"`
	Key             types.String  `tfsdk:"key"`
	CurrentHost     types.Bool    `tfsdk:"current_host"`
	Value           types.Dynamic `tfsdk:"value"`
	DeleteOnDestroy types.Bool    `tfsdk:"delete_on_destroy"`
	Restart         types.List    `tfsdk:"restart"`
}

type macOSDefaultsNamedSpec struct {
	Name      string
	GroupName string
	Model     MacOSDefaultsDefaultModel
	Spec      macOSDefaultSpec
}

func NewMacOSDefaultsResource() resource.Resource {
	return &MacOSDefaultsResource{}
}

func (r *MacOSDefaultsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mac_settings"
}

func (r *MacOSDefaultsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages multiple macOS settings backed by `defaults` keys as one grouped Terraform resource.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, always `mac_settings`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"settings": schema.DynamicAttribute{
				Optional:            true,
				MarkdownDescription: "Named macOS settings to manage. Each map value is an object with string `domain`, `key`, and `value`, plus optional `current_host`, `delete_on_destroy`, and `restart`.",
			},
			"groups": schema.DynamicAttribute{
				Optional:            true,
				MarkdownDescription: "macOS settings grouped by exact defaults domain. Each `groups` map key is the exact defaults domain, such as `com.apple.dock`, `NSGlobalDomain`, or an application bundle identifier. Each group value is a map from defaults key to setting value.",
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
			resp.Diagnostics.AddError("macOS settings unavailable", "`host_mac_settings` requires the macOS `defaults` command.")
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

	plan.ID = types.StringValue(macOSSettingsResourceID)
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
		resp.Diagnostics.AddError("Failed to sync macOS settings", err.Error())
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
		resp.Diagnostics.AddError("Failed to read macOS settings", err.Error())
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
		resp.Diagnostics.AddError("Failed to sync macOS settings", err.Error())
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
		resp.Diagnostics.AddError("macOS settings unavailable", "`host_mac_settings` requires the macOS `defaults` command.")
		return
	}

	var restartProcesses []string
	for _, spec := range specs {
		if !spec.Spec.DeleteOnDestroy {
			continue
		}
		if err := r.manager.DeleteDefault(ctx, spec.Spec); err != nil {
			resp.Diagnostics.AddError("Failed to delete macOS setting", err.Error())
			return
		}
		restartProcesses = append(restartProcesses, spec.Spec.Restart...)
	}

	if err := r.manager.RestartProcesses(ctx, uniqueStrings(restartProcesses)); err != nil {
		resp.Diagnostics.AddError("Failed to restart macOS processes", err.Error())
	}
}

func (r *MacOSDefaultsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	state, err := r.importDefaultsState(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import macOS settings", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSDefaultsResource) importDefaultsState(ctx context.Context, importID string) (MacOSDefaultsResourceModel, error) {
	if r.manager == nil {
		return MacOSDefaultsResourceModel{}, fmt.Errorf("macOS defaults manager unavailable")
	}

	specs, err := macOSDefaultsImportSpecs(importID)
	if err != nil {
		return MacOSDefaultsResourceModel{}, err
	}

	for i := range specs {
		value, exists, err := r.manager.ReadDefault(ctx, specs[i].Spec)
		if err != nil {
			return MacOSDefaultsResourceModel{}, err
		}
		if !exists {
			return MacOSDefaultsResourceModel{}, fmt.Errorf("no value exists for %q", specs[i].Spec.ID)
		}
		specs[i].Spec.Value = value
	}

	return macOSDefaultsModelFromSpecs(ctx, specs)
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
	if !macOSSettingsDynamicReady(model.Settings) {
		return false
	}
	if !macOSSettingsDynamicReady(model.Groups) {
		return false
	}
	return true
}

func macOSSettingsDynamicReady(value types.Dynamic) bool {
	if value.IsUnknown() {
		return false
	}
	if value.IsNull() {
		return true
	}
	return !value.IsUnderlyingValueUnknown() && macOSSettingsValueReady(value.UnderlyingValue())
}

func macOSSettingsValueReady(value attr.Value) bool {
	if value.IsUnknown() {
		return false
	}
	if dynamic, ok := value.(types.Dynamic); ok {
		return !dynamic.IsUnderlyingValueUnknown()
	}
	object, ok := value.(types.Object)
	if !ok {
		return true
	}
	for _, attrValue := range object.Attributes() {
		if !macOSSettingsValueReady(attrValue) {
			return false
		}
	}
	return true
}

func macOSDefaultsSpecsFromModel(ctx context.Context, model MacOSDefaultsResourceModel) ([]macOSDefaultsNamedSpec, diag.Diagnostics) {
	var diags diag.Diagnostics
	if model.Settings.IsNull() && model.Groups.IsNull() {
		diags.AddError("Invalid macOS settings", "Set at least one of settings or groups.")
		return nil, diags
	}
	if model.Settings.IsUnknown() || model.Groups.IsUnknown() {
		diags.AddError("Invalid macOS settings", "settings and groups must be known")
		return nil, diags
	}

	specs := macOSDefaultsFlatSpecsFromModel(ctx, model, &diags)
	specs = append(specs, macOSDefaultsGroupedSpecsFromModel(ctx, model, &diags)...)
	if diags.HasError() {
		return nil, diags
	}

	seenIDs := make(map[string]string, len(specs))
	for _, spec := range specs {
		displayName := macOSDefaultsSpecDisplayName(spec)
		if previousName, ok := seenIDs[spec.Spec.ID]; ok {
			diags.AddError(
				"Duplicate macOS setting",
				fmt.Sprintf("%s and %s both manage %q.", previousName, displayName, spec.Spec.ID),
			)
			continue
		}
		seenIDs[spec.Spec.ID] = displayName
	}
	return specs, diags
}

func macOSDefaultsFlatSpecsFromModel(ctx context.Context, model MacOSDefaultsResourceModel, diags *diag.Diagnostics) []macOSDefaultsNamedSpec {
	if model.Settings.IsNull() {
		return nil
	}
	settings := macOSSettingsElementsFromDynamic(model.Settings, "settings", diags)
	if diags.HasError() {
		return nil
	}

	names := make([]string, 0, len(settings))
	for name := range settings {
		names = append(names, name)
	}
	sort.Strings(names)

	specs := make([]macOSDefaultsNamedSpec, 0, len(settings))
	for _, name := range names {
		item := macOSDefaultsDefaultModelFromValue(ctx, settings[name], "settings."+name, diags)
		if diags.HasError() {
			continue
		}
		spec, specDiags := macOSDefaultSpecFromModel(ctx, MacOSDefaultResourceModel{
			Domain:          item.Domain,
			Key:             item.Key,
			CurrentHost:     item.CurrentHost,
			Value:           item.Value,
			DeleteOnDestroy: item.DeleteOnDestroy,
			Restart:         item.Restart,
		})
		diags.Append(specDiags...)
		if specDiags.HasError() {
			continue
		}

		specs = append(specs, macOSDefaultsNamedSpec{
			Name:  name,
			Model: item,
			Spec:  spec,
		})
	}
	return specs
}

func macOSDefaultsGroupedSpecsFromModel(ctx context.Context, model MacOSDefaultsResourceModel, diags *diag.Diagnostics) []macOSDefaultsNamedSpec {
	if model.Groups.IsNull() {
		return nil
	}
	groups := macOSSettingsElementsFromDynamic(model.Groups, "groups", diags)
	if diags.HasError() {
		return nil
	}

	groupNames := make([]string, 0, len(groups))
	for name := range groups {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)

	var specs []macOSDefaultsNamedSpec
	for _, groupName := range groupNames {
		groupDomain, err := macOSSettingsGroupDomain(groupName)
		if err != nil {
			diags.AddError("Invalid macOS settings group", fmt.Sprintf("groups.%s: %s", groupName, err.Error()))
			continue
		}

		settings := macOSSettingsElementsFromValue(groups[groupName], "groups."+groupName, diags)
		if diags.HasError() {
			continue
		}

		names := make([]string, 0, len(settings))
		for name := range settings {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			value := macOSSettingsGroupSettingValueFromValue(settings[name], "groups."+groupName+"."+name, diags)
			if diags.HasError() {
				continue
			}
			defaultModel := MacOSDefaultsDefaultModel{
				Domain:          types.StringValue(groupDomain),
				Key:             types.StringValue(name),
				CurrentHost:     types.BoolNull(),
				Value:           value,
				DeleteOnDestroy: types.BoolNull(),
				Restart:         types.ListNull(types.StringType),
			}
			spec, specDiags := macOSDefaultSpecFromModel(ctx, MacOSDefaultResourceModel{
				Domain:          defaultModel.Domain,
				Key:             defaultModel.Key,
				CurrentHost:     defaultModel.CurrentHost,
				Value:           defaultModel.Value,
				DeleteOnDestroy: defaultModel.DeleteOnDestroy,
				Restart:         defaultModel.Restart,
			})
			diags.Append(specDiags...)
			if specDiags.HasError() {
				continue
			}

			specs = append(specs, macOSDefaultsNamedSpec{
				Name:      name,
				GroupName: groupName,
				Model:     defaultModel,
				Spec:      spec,
			})
		}
	}
	return specs
}

func macOSDefaultsSpecDisplayName(spec macOSDefaultsNamedSpec) string {
	if spec.GroupName == "" {
		return "settings." + spec.Name
	}
	return "groups." + spec.GroupName + "." + spec.Name
}

func macOSSettingsElementsFromDynamic(value types.Dynamic, name string, diags *diag.Diagnostics) map[string]attr.Value {
	if value.IsNull() {
		return nil
	}
	if value.IsUnknown() || value.IsUnderlyingValueUnknown() {
		diags.AddError("Invalid macOS settings", name+" must be known.")
		return nil
	}
	if value.IsUnderlyingValueNull() {
		return nil
	}
	return macOSSettingsElementsFromValue(value.UnderlyingValue(), name, diags)
}

func macOSSettingsElementsFromValue(value attr.Value, name string, diags *diag.Diagnostics) map[string]attr.Value {
	value = macOSSettingsUnwrapDynamic(value)
	switch typed := value.(type) {
	case types.Object:
		if typed.IsNull() {
			return nil
		}
		if typed.IsUnknown() {
			diags.AddError("Invalid macOS settings", name+" must be known.")
			return nil
		}
		return typed.Attributes()
	case types.Map:
		if typed.IsNull() {
			return nil
		}
		if typed.IsUnknown() {
			diags.AddError("Invalid macOS settings", name+" must be known.")
			return nil
		}
		return typed.Elements()
	default:
		diags.AddError("Invalid macOS settings", fmt.Sprintf("%s must be an object or map, got %T.", name, value))
		return nil
	}
}

func macOSDefaultsDefaultModelFromValue(ctx context.Context, value attr.Value, name string, diags *diag.Diagnostics) MacOSDefaultsDefaultModel {
	attrs := macOSSettingsObjectAttrs(value, name, diags)
	if diags.HasError() {
		return MacOSDefaultsDefaultModel{}
	}

	return MacOSDefaultsDefaultModel{
		Domain:          macOSSettingsStringAttr(attrs, "domain", name, true, diags),
		Key:             macOSSettingsStringAttr(attrs, "key", name, true, diags),
		CurrentHost:     macOSSettingsBoolAttr(attrs, "current_host", name, false, diags),
		Value:           macOSSettingsValueAttr(attrs, "value", name, diags),
		DeleteOnDestroy: macOSSettingsBoolAttr(attrs, "delete_on_destroy", name, false, diags),
		Restart:         macOSSettingsStringListAttr(ctx, attrs, "restart", name, false, diags),
	}
}

func macOSSettingsGroupDomain(groupName string) (string, error) {
	name := strings.TrimSpace(groupName)
	if name == "" {
		return "", fmt.Errorf("domain must be non-empty")
	}

	if err := validateMacOSSettingDomain(name); err != nil {
		return "", err
	}
	return name, nil
}

func macOSSettingsGroupSettingValueFromValue(value attr.Value, name string, diags *diag.Diagnostics) types.Dynamic {
	value = macOSSettingsUnwrapDynamic(value)
	if value.IsNull() || value.IsUnknown() {
		diags.AddError("Invalid macOS setting", name+" must be known and non-null.")
		return types.DynamicNull()
	}

	return types.DynamicValue(value)
}

func macOSSettingsObjectAttrs(value attr.Value, name string, diags *diag.Diagnostics) map[string]attr.Value {
	value = macOSSettingsUnwrapDynamic(value)
	object, ok := value.(types.Object)
	if !ok {
		diags.AddError("Invalid macOS setting", fmt.Sprintf("%s must be an object, got %T.", name, value))
		return nil
	}
	if object.IsNull() || object.IsUnknown() {
		diags.AddError("Invalid macOS setting", name+" must be a known non-null object.")
		return nil
	}
	return object.Attributes()
}

func macOSSettingsStringAttr(attrs map[string]attr.Value, attrName string, objectName string, required bool, diags *diag.Diagnostics) types.String {
	raw, ok := attrs[attrName]
	if !ok {
		if required {
			diags.AddError("Invalid macOS setting", objectName+"."+attrName+" is required.")
		}
		return types.StringNull()
	}
	value := macOSSettingsUnwrapDynamic(raw)
	stringValue, ok := value.(types.String)
	if !ok || stringValue.IsUnknown() {
		diags.AddError("Invalid macOS setting", objectName+"."+attrName+" must be a known string.")
		return types.StringNull()
	}
	if required && stringValue.IsNull() {
		diags.AddError("Invalid macOS setting", objectName+"."+attrName+" must not be null.")
	}
	return stringValue
}

func macOSSettingsBoolAttr(attrs map[string]attr.Value, attrName string, objectName string, required bool, diags *diag.Diagnostics) types.Bool {
	raw, ok := attrs[attrName]
	if !ok {
		if required {
			diags.AddError("Invalid macOS setting", objectName+"."+attrName+" is required.")
		}
		return types.BoolNull()
	}
	value := macOSSettingsUnwrapDynamic(raw)
	boolValue, ok := value.(types.Bool)
	if !ok || boolValue.IsUnknown() {
		diags.AddError("Invalid macOS setting", objectName+"."+attrName+" must be a known bool.")
		return types.BoolNull()
	}
	if required && boolValue.IsNull() {
		diags.AddError("Invalid macOS setting", objectName+"."+attrName+" must not be null.")
	}
	return boolValue
}

func macOSSettingsValueAttr(attrs map[string]attr.Value, attrName string, objectName string, diags *diag.Diagnostics) types.Dynamic {
	raw, ok := attrs[attrName]
	if !ok {
		diags.AddError("Invalid macOS setting", objectName+"."+attrName+" is required.")
		return types.DynamicNull()
	}
	value := macOSSettingsUnwrapDynamic(raw)
	if value.IsNull() || value.IsUnknown() {
		diags.AddError("Invalid macOS setting", objectName+"."+attrName+" must be known and non-null.")
		return types.DynamicNull()
	}
	return types.DynamicValue(value)
}

func macOSSettingsStringListAttr(ctx context.Context, attrs map[string]attr.Value, attrName string, objectName string, required bool, diags *diag.Diagnostics) types.List {
	raw, ok := attrs[attrName]
	if !ok {
		if required {
			diags.AddError("Invalid macOS setting", objectName+"."+attrName+" is required.")
		}
		return types.ListNull(types.StringType)
	}
	value := macOSSettingsUnwrapDynamic(raw)
	if value.IsNull() {
		return types.ListNull(types.StringType)
	}
	if value.IsUnknown() {
		diags.AddError("Invalid macOS setting", objectName+"."+attrName+" must be known.")
		return types.ListNull(types.StringType)
	}

	var elements []string
	var err error
	switch typed := value.(type) {
	case types.List:
		elements, err = macOSDefaultStringSliceFromElements(typed.Elements())
	case types.Tuple:
		elements, err = macOSDefaultStringSliceFromElements(typed.Elements())
	default:
		diags.AddError("Invalid macOS setting", fmt.Sprintf("%s.%s must be a list of strings, got %T.", objectName, attrName, value))
		return types.ListNull(types.StringType)
	}
	if err != nil {
		diags.AddError("Invalid macOS setting", objectName+"."+attrName+": "+err.Error())
		return types.ListNull(types.StringType)
	}

	list, listDiags := types.ListValueFrom(ctx, types.StringType, elements)
	diags.Append(listDiags...)
	if listDiags.HasError() {
		return types.ListNull(types.StringType)
	}
	return list
}

func macOSSettingsUnwrapDynamic(value attr.Value) attr.Value {
	for {
		dynamic, ok := value.(types.Dynamic)
		if !ok || dynamic.IsNull() || dynamic.IsUnknown() || dynamic.IsUnderlyingValueNull() || dynamic.IsUnderlyingValueUnknown() {
			return value
		}
		value = dynamic.UnderlyingValue()
	}
}

func macOSDefaultsImportSpecs(importID string) ([]macOSDefaultsNamedSpec, error) {
	raw := strings.TrimSpace(importID)
	if raw == "" {
		return nil, fmt.Errorf("import ID must be non-empty")
	}

	parts := strings.Split(raw, ",")
	specs := make([]macOSDefaultsNamedSpec, 0, len(parts))
	seenNames := make(map[string]struct{}, len(parts))
	seenIDs := make(map[string]string, len(parts))
	groupScopes := make(map[string]string, len(parts))
	for _, part := range parts {
		entry := strings.TrimSpace(part)
		name, defaultID, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("expected import entry %q to be in the form `<map_key>=<default_id>`", entry)
		}

		name = strings.TrimSpace(name)
		defaultID = strings.TrimSpace(defaultID)
		if name == "" {
			return nil, fmt.Errorf("import map key must be non-empty")
		}
		if strings.ContainsAny(name, "\x00\r\n,=") {
			return nil, fmt.Errorf("import map key %q must not contain control characters, comma, or equals", name)
		}
		if _, ok := seenNames[name]; ok {
			return nil, fmt.Errorf("duplicate import map key %q", name)
		}
		seenNames[name] = struct{}{}

		spec, err := macOSDefaultImportSpec(defaultID)
		if err != nil {
			return nil, err
		}
		if previousName, ok := seenIDs[spec.ID]; ok {
			return nil, fmt.Errorf("settings.%s and settings.%s both import %q", previousName, name, spec.ID)
		}
		seenIDs[spec.ID] = name

		groupName, settingName, grouped := strings.Cut(name, "/")
		if grouped {
			groupName = strings.TrimSpace(groupName)
			settingName = strings.TrimSpace(settingName)
			if groupName == "" || settingName == "" {
				return nil, fmt.Errorf("import map key %q must use non-empty group and setting names", name)
			}
			groupDomain, err := macOSSettingsGroupDomain(groupName)
			if err != nil {
				return nil, fmt.Errorf("import group key %q is invalid: %w", groupName, err)
			}
			if groupDomain != spec.Domain {
				return nil, fmt.Errorf("import group key %q resolves to %q, but %q imports %q", groupName, groupDomain, name, spec.Domain)
			}
			if spec.CurrentHost {
				return nil, fmt.Errorf("group import key %q uses currentHost scope, which is only supported by top-level settings", name)
			}
			groupScope := macOSDefaultID(spec.Domain, "", spec.CurrentHost)
			if previousScope, ok := groupScopes[groupName]; ok && previousScope != groupScope {
				return nil, fmt.Errorf("group import key %q uses a different domain or scope than other entries in group %q", name, groupName)
			}
			groupScopes[groupName] = groupScope
		}

		currentHostValue := types.BoolNull()
		if spec.CurrentHost {
			currentHostValue = types.BoolValue(true)
		}

		namedSpec := macOSDefaultsNamedSpec{
			Name: name,
			Model: MacOSDefaultsDefaultModel{
				Domain:          types.StringValue(spec.Domain),
				Key:             types.StringValue(spec.Key),
				CurrentHost:     currentHostValue,
				DeleteOnDestroy: types.BoolNull(),
				Restart:         types.ListNull(types.StringType),
			},
			Spec: spec,
		}
		if grouped {
			namedSpec.Name = settingName
			namedSpec.GroupName = groupName
		}

		specs = append(specs, namedSpec)
	}

	return specs, nil
}

func macOSDefaultsModelFromSpecs(ctx context.Context, specs []macOSDefaultsNamedSpec) (MacOSDefaultsResourceModel, error) {
	settingsElements := make(map[string]attr.Value, len(specs))
	groupSpecs := make(map[string][]macOSDefaultsNamedSpec)
	for _, spec := range specs {
		model, err := macOSDefaultsDefaultModelWithValue(spec.Model, spec.Spec.Value)
		if err != nil {
			return MacOSDefaultsResourceModel{}, err
		}
		spec.Model = model
		if spec.GroupName != "" {
			groupSpecs[spec.GroupName] = append(groupSpecs[spec.GroupName], spec)
			continue
		}
		objectValue, err := macOSDefaultsDefaultObjectValue(ctx, model)
		if err != nil {
			return MacOSDefaultsResourceModel{}, err
		}
		settingsElements[spec.Name] = objectValue
	}

	settings := types.DynamicNull()
	if len(settingsElements) > 0 {
		settingsValue, err := macOSSettingsDynamicObject(ctx, settingsElements)
		if err != nil {
			return MacOSDefaultsResourceModel{}, err
		}
		settings = settingsValue
	}

	groups, err := macOSSettingsGroupsModelFromSpecs(ctx, groupSpecs)
	if err != nil {
		return MacOSDefaultsResourceModel{}, err
	}

	return MacOSDefaultsResourceModel{
		ID:       types.StringValue(macOSSettingsResourceID),
		Settings: settings,
		Groups:   groups,
	}, nil
}

func macOSDefaultsDefaultObjectValue(ctx context.Context, model MacOSDefaultsDefaultModel) (types.Object, error) {
	attrs := map[string]attr.Value{
		"domain": model.Domain,
		"key":    model.Key,
		"value":  macOSSettingsDynamicUnderlying(model.Value),
	}
	macOSSettingsSetOptionalAttr(attrs, "current_host", model.CurrentHost)
	macOSSettingsSetOptionalAttr(attrs, "delete_on_destroy", model.DeleteOnDestroy)
	macOSSettingsSetOptionalAttr(attrs, "restart", model.Restart)
	return macOSSettingsObjectValue(ctx, attrs)
}

func macOSSettingsGroupsModelFromSpecs(ctx context.Context, groups map[string][]macOSDefaultsNamedSpec) (types.Dynamic, error) {
	if len(groups) == 0 {
		return types.DynamicNull(), nil
	}

	groupElements := make(map[string]attr.Value, len(groups))
	for groupName, specs := range groups {
		if len(specs) == 0 {
			continue
		}
		settingElements := make(map[string]attr.Value, len(specs))
		for _, spec := range specs {
			settingElements[spec.Name] = macOSSettingsDynamicUnderlying(spec.Model.Value)
		}

		groupValue, err := macOSSettingsObjectValue(ctx, settingElements)
		if err != nil {
			return types.Dynamic{}, err
		}
		groupElements[groupName] = groupValue
	}

	groupsValue, err := macOSSettingsDynamicObject(ctx, groupElements)
	if err != nil {
		return types.Dynamic{}, err
	}
	return groupsValue, nil
}

func macOSSettingsDynamicObject(ctx context.Context, elements map[string]attr.Value) (types.Dynamic, error) {
	object, err := macOSSettingsObjectValue(ctx, elements)
	if err != nil {
		return types.Dynamic{}, err
	}
	return types.DynamicValue(object), nil
}

func macOSSettingsObjectValue(ctx context.Context, attrs map[string]attr.Value) (types.Object, error) {
	attrTypes := make(map[string]attr.Type, len(attrs))
	for name, value := range attrs {
		attrTypes[name] = value.Type(ctx)
	}
	objectValue, diags := types.ObjectValue(attrTypes, attrs)
	if diags.HasError() {
		return types.Object{}, diagnosticsError(diags)
	}
	return objectValue, nil
}

func macOSSettingsDynamicUnderlying(value types.Dynamic) attr.Value {
	if value.IsNull() || value.IsUnknown() || value.IsUnderlyingValueNull() || value.IsUnderlyingValueUnknown() {
		return value
	}
	return value.UnderlyingValue()
}

func macOSSettingsSetOptionalAttr(attrs map[string]attr.Value, name string, value attr.Value) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	attrs[name] = value
}

func macOSDefaultsDefaultModelWithValue(model MacOSDefaultsDefaultModel, value macOSDefaultValue) (MacOSDefaultsDefaultModel, error) {
	resourceModel := MacOSDefaultResourceModel{
		Domain:          model.Domain,
		Key:             model.Key,
		CurrentHost:     model.CurrentHost,
		Value:           model.Value,
		DeleteOnDestroy: model.DeleteOnDestroy,
		Restart:         model.Restart,
	}
	resourceModel, err := macOSDefaultModelWithValue(resourceModel, value)
	if err != nil {
		return model, err
	}

	model.Value = resourceModel.Value
	return model, nil
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
