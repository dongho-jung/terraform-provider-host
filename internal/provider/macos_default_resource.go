package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"os/exec"
	"reflect"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &MacOSDefaultResource{}
	_ resource.ResourceWithConfigure   = &MacOSDefaultResource{}
	_ resource.ResourceWithImportState = &MacOSDefaultResource{}
	_ resource.ResourceWithModifyPlan  = &MacOSDefaultResource{}
)

const (
	macOSDefaultValueBool       = "bool"
	macOSDefaultValueInt        = "int"
	macOSDefaultValueFloat      = "float"
	macOSDefaultValueString     = "string"
	macOSDefaultValueStringList = "string_list"
)

type MacOSDefaultResource struct {
	manager MacOSDefaultsManager
}

type MacOSDefaultResourceModel struct {
	ID              types.String  `tfsdk:"id"`
	Domain          types.Object  `tfsdk:"domain"`
	DomainResolved  types.String  `tfsdk:"domain_resolved"`
	Key             types.String  `tfsdk:"key"`
	CurrentHost     types.Bool    `tfsdk:"current_host"`
	Value           types.Dynamic `tfsdk:"value"`
	DeleteOnDestroy types.Bool    `tfsdk:"delete_on_destroy"`
	Restart         types.List    `tfsdk:"restart"`
}

type MacOSSettingDomainModel struct {
	Apple  types.String `tfsdk:"apple"`
	Global types.Bool   `tfsdk:"global"`
	Raw    types.String `tfsdk:"raw"`
}

type macOSDefaultSpec struct {
	ID              string
	Domain          string
	Key             string
	CurrentHost     bool
	DeleteOnDestroy bool
	Restart         []string
	Value           macOSDefaultValue
}

type macOSDefaultValue struct {
	Type       string
	Bool       bool
	Int        int64
	Float      float64
	String     string
	StringList []string
}

type MacOSDefaultsManager interface {
	ReadDefault(ctx context.Context, spec macOSDefaultSpec) (macOSDefaultValue, bool, error)
	WriteDefault(ctx context.Context, spec macOSDefaultSpec) error
	DeleteDefault(ctx context.Context, spec macOSDefaultSpec) error
	RestartProcesses(ctx context.Context, processNames []string) error
}

type CLIMacOSDefaultsManager struct {
	defaultsPath string
	killallPath  string
	run          macOSCommandRunner
}

type macOSCommandRunner func(ctx context.Context, command string, args ...string) ([]byte, error)

func NewCLIMacOSDefaultsManager(defaultsPath string, killallPath string) MacOSDefaultsManager {
	return &CLIMacOSDefaultsManager{
		defaultsPath: defaultsPath,
		killallPath:  killallPath,
		run:          runMacOSCommand,
	}
}

func NewMacOSDefaultResource() resource.Resource {
	return &MacOSDefaultResource{}
}

func (r *MacOSDefaultResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mac_setting"
}

func (r *MacOSDefaultResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one macOS setting backed by a `defaults` key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier derived from `domain`, `key`, and `current_host`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"domain": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "macOS defaults domain selector. Set exactly one of `apple`, `global`, or `raw`.",
				Attributes:          macOSSettingDomainSchemaAttributes(),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
			},
			"domain_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved macOS defaults domain, such as `com.apple.dock` or `NSGlobalDomain`.",
			},
			"key": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Defaults key to manage inside the domain.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"current_host": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Use `defaults -currentHost` for host-specific preferences.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"value": schema.DynamicAttribute{
				Required:            true,
				MarkdownDescription: "Setting value. Supported values are bool, number, string, and a list or tuple of strings. Whole numbers are written as integer defaults values; fractional numbers are written as float defaults values.",
			},
			"delete_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Delete the managed defaults key on destroy. Defaults to false, leaving the current macOS setting in place.",
			},
			"restart": schema.ListAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				MarkdownDescription: "Process names to restart with `killall` after writes or deletes, such as `Dock`, `Finder`, or `SystemUIServer`. Omit to use provider defaults for known domains; set `[]` to disable restarts.",
			},
		},
	}
}

func macOSSettingDomainSchemaAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"apple": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Apple defaults domain suffix without the `com.apple.` prefix, such as `dock`, `screencapture`, or `AppleMultitouchTrackpad`.",
		},
		"global": schema.BoolAttribute{
			Optional:            true,
			MarkdownDescription: "Use `NSGlobalDomain` when true.",
		},
		"raw": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Full raw defaults domain for non-Apple domains or domains that should not be expanded.",
		},
	}
}

func macOSSettingDomainAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"apple":  types.StringType,
		"global": types.BoolType,
		"raw":    types.StringType,
	}
}

func macOSSettingDomainObjectFromResolved(domain string) (types.Object, error) {
	values := map[string]attr.Value{
		"apple":  types.StringNull(),
		"global": types.BoolNull(),
		"raw":    types.StringNull(),
	}
	switch {
	case domain == "NSGlobalDomain":
		values["global"] = types.BoolValue(true)
	case strings.HasPrefix(domain, "com.apple."):
		values["apple"] = types.StringValue(strings.TrimPrefix(domain, "com.apple."))
	default:
		values["raw"] = types.StringValue(domain)
	}

	object, diags := types.ObjectValue(macOSSettingDomainAttributeTypes(), values)
	if diags.HasError() {
		return types.Object{}, diagnosticsError(diags)
	}
	return object, nil
}

func macOSSettingDomainObjectWithDefaults(value types.Object, diags *diag.Diagnostics) types.Object {
	values := map[string]attr.Value{
		"apple":  types.StringNull(),
		"global": types.BoolNull(),
		"raw":    types.StringNull(),
	}
	for name, attrValue := range value.Attributes() {
		if _, ok := values[name]; !ok {
			diags.AddError("Invalid macOS setting domain", "Unknown domain attribute "+name+".")
			continue
		}
		values[name] = attrValue
	}

	object, objectDiags := types.ObjectValue(macOSSettingDomainAttributeTypes(), values)
	diags.Append(objectDiags...)
	if objectDiags.HasError() {
		return types.ObjectNull(macOSSettingDomainAttributeTypes())
	}
	return object
}

func macOSSettingDomainFromObject(ctx context.Context, value types.Object) (string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if value.IsNull() {
		diags.AddError("Invalid macOS setting domain", "domain must not be null")
		return "", diags
	}
	if value.IsUnknown() {
		diags.AddError("Invalid macOS setting domain", "domain must be known")
		return "", diags
	}

	value = macOSSettingDomainObjectWithDefaults(value, &diags)
	if diags.HasError() {
		return "", diags
	}

	var model MacOSSettingDomainModel
	diags.Append(value.As(ctx, &model, basetypesObjectAsOptions())...)
	if diags.HasError() {
		return "", diags
	}

	values := []struct {
		name  string
		value string
	}{
		{name: "apple", value: model.Apple.ValueString()},
		{name: "raw", value: model.Raw.ValueString()},
	}

	var kind string
	var raw string
	for _, candidate := range values {
		trimmed := strings.TrimSpace(candidate.value)
		if trimmed == "" || candidate.name == "apple" && (model.Apple.IsNull() || model.Apple.IsUnknown()) || candidate.name == "raw" && (model.Raw.IsNull() || model.Raw.IsUnknown()) {
			continue
		}
		if kind != "" {
			diags.AddError("Invalid macOS setting domain", "Set exactly one of domain.apple, domain.global, or domain.raw.")
			return "", diags
		}
		kind = candidate.name
		raw = trimmed
	}
	if !model.Global.IsNull() && !model.Global.IsUnknown() && model.Global.ValueBool() {
		if kind != "" {
			diags.AddError("Invalid macOS setting domain", "Set exactly one of domain.apple, domain.global, or domain.raw.")
			return "", diags
		}
		kind = "global"
		raw = "true"
	}
	if kind == "" {
		diags.AddError("Invalid macOS setting domain", "Set exactly one of domain.apple, domain.global, or domain.raw.")
		return "", diags
	}

	var domain string
	switch kind {
	case "apple":
		domain = macOSSettingAppleDomain(raw)
	case "global":
		domain = "NSGlobalDomain"
	case "raw":
		domain = raw
	}
	if err := validateMacOSSettingDomain(domain); err != nil {
		diags.AddError("Invalid macOS setting domain", err.Error())
		return "", diags
	}
	return domain, diags
}

func macOSSettingAppleDomain(value string) string {
	suffix := strings.TrimSpace(value)
	if strings.HasPrefix(suffix, "com.apple.") {
		return suffix
	}
	return "com.apple." + suffix
}

func validateMacOSSettingDomain(domain string) error {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return fmt.Errorf("domain must be non-empty")
	}
	if strings.ContainsAny(domain, "\x00\r\n") {
		return fmt.Errorf("domain must not contain control characters")
	}
	return nil
}

func (r *MacOSDefaultResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.MacOSDefaultsManager == nil {
			resp.Diagnostics.AddError("macOS settings unavailable", "`host_mac_setting` requires the macOS `defaults` command.")
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

func (r *MacOSDefaultResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan MacOSDefaultResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !macOSDefaultPlanReady(plan) {
		return
	}

	spec, diags := macOSDefaultSpecFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = types.StringValue(spec.ID)
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *MacOSDefaultResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MacOSDefaultResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncDefault(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync macOS setting", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSDefaultResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MacOSDefaultResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, exists, err := r.readDefault(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read macOS setting", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *MacOSDefaultResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MacOSDefaultResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncDefault(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync macOS setting", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSDefaultResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MacOSDefaultResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	spec, diags := macOSDefaultSpecFromModel(ctx, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if spec.DeleteOnDestroy {
		if r.manager == nil {
			resp.Diagnostics.AddError("macOS settings unavailable", "`host_mac_setting` requires the macOS `defaults` command.")
			return
		}
		if err := r.manager.DeleteDefault(ctx, spec); err != nil {
			resp.Diagnostics.AddError("Failed to delete macOS setting", err.Error())
			return
		}
		if err := r.manager.RestartProcesses(ctx, spec.Restart); err != nil {
			resp.Diagnostics.AddError("Failed to restart macOS processes", err.Error())
		}
	}
}

func (r *MacOSDefaultResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	state, err := r.importDefaultState(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import macOS setting", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSDefaultResource) importDefaultState(ctx context.Context, importID string) (MacOSDefaultResourceModel, error) {
	if r.manager == nil {
		return MacOSDefaultResourceModel{}, fmt.Errorf("macOS defaults manager unavailable")
	}

	spec, err := macOSDefaultImportSpec(importID)
	if err != nil {
		return MacOSDefaultResourceModel{}, err
	}

	value, exists, err := r.manager.ReadDefault(ctx, spec)
	if err != nil {
		return MacOSDefaultResourceModel{}, err
	}
	if !exists {
		return MacOSDefaultResourceModel{}, fmt.Errorf("no value exists for %q", importID)
	}

	domainObject, err := macOSSettingDomainObjectFromResolved(spec.Domain)
	if err != nil {
		return MacOSDefaultResourceModel{}, err
	}
	state := MacOSDefaultResourceModel{
		ID:              types.StringValue(spec.ID),
		Domain:          domainObject,
		DomainResolved:  types.StringValue(spec.Domain),
		Key:             types.StringValue(spec.Key),
		CurrentHost:     types.BoolValue(spec.CurrentHost),
		DeleteOnDestroy: types.BoolValue(false),
		Restart:         types.ListNull(types.StringType),
	}
	return macOSDefaultModelWithValue(ctx, state, value)
}

func (r *MacOSDefaultResource) syncDefault(ctx context.Context, model MacOSDefaultResourceModel) (MacOSDefaultResourceModel, error) {
	if r.manager == nil {
		return model, fmt.Errorf("macOS defaults manager unavailable")
	}

	spec, diags := macOSDefaultSpecFromModel(ctx, model)
	if diags.HasError() {
		return model, diagnosticsError(diags)
	}

	if err := r.manager.WriteDefault(ctx, spec); err != nil {
		return model, err
	}
	if err := r.manager.RestartProcesses(ctx, spec.Restart); err != nil {
		return model, err
	}

	return macOSDefaultModelWithValue(ctx, model, spec.Value)
}

func (r *MacOSDefaultResource) readDefault(ctx context.Context, model MacOSDefaultResourceModel) (MacOSDefaultResourceModel, bool, error) {
	if r.manager == nil {
		return model, false, fmt.Errorf("macOS defaults manager unavailable")
	}

	spec, diags := macOSDefaultSpecFromModel(ctx, model)
	if diags.HasError() {
		return model, false, diagnosticsError(diags)
	}

	actual, exists, err := r.manager.ReadDefault(ctx, spec)
	if err != nil || !exists {
		return model, exists, err
	}

	next, err := macOSDefaultModelWithValue(ctx, model, actual)
	if err != nil {
		return model, false, err
	}
	next.ID = types.StringValue(spec.ID)
	return next, true, nil
}

func (m *CLIMacOSDefaultsManager) ReadDefault(ctx context.Context, spec macOSDefaultSpec) (macOSDefaultValue, bool, error) {
	if m.defaultsPath == "" {
		return macOSDefaultValue{}, false, fmt.Errorf("defaults command not found")
	}

	typeArgs := m.defaultsArgs(spec.CurrentHost, "read-type", spec.Domain, spec.Key)
	typeOut, err := m.run(ctx, m.defaultsPath, typeArgs...)
	if err != nil {
		if isMacOSDefaultsMissingError(err) {
			return macOSDefaultValue{}, false, nil
		}
		return macOSDefaultValue{}, false, err
	}
	valueType, err := parseMacOSDefaultsReadType(string(typeOut))
	if err != nil {
		return macOSDefaultValue{}, false, err
	}

	readArgs := m.defaultsArgs(spec.CurrentHost, "read", spec.Domain, spec.Key)
	valueOut, err := m.run(ctx, m.defaultsPath, readArgs...)
	if err != nil {
		if isMacOSDefaultsMissingError(err) {
			return macOSDefaultValue{}, false, nil
		}
		return macOSDefaultValue{}, false, err
	}
	value, err := parseMacOSDefaultReadValue(valueType, string(valueOut))
	if err != nil {
		return macOSDefaultValue{}, false, err
	}
	return value, true, nil
}

func isMacOSDefaultsMissingError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "does not exist") ||
		strings.Contains(message, "Domain ") && strings.Contains(message, " does not exist")
}

func (m *CLIMacOSDefaultsManager) WriteDefault(ctx context.Context, spec macOSDefaultSpec) error {
	if m.defaultsPath == "" {
		return fmt.Errorf("defaults command not found")
	}

	args := m.defaultsArgs(spec.CurrentHost, "write", spec.Domain, spec.Key)
	args = append(args, macOSDefaultWriteArgs(spec.Value)...)
	_, err := m.run(ctx, m.defaultsPath, args...)
	return err
}

func (m *CLIMacOSDefaultsManager) DeleteDefault(ctx context.Context, spec macOSDefaultSpec) error {
	if m.defaultsPath == "" {
		return fmt.Errorf("defaults command not found")
	}

	args := m.defaultsArgs(spec.CurrentHost, "delete", spec.Domain, spec.Key)
	_, err := m.run(ctx, m.defaultsPath, args...)
	if isMacOSDefaultsMissingError(err) {
		return nil
	}
	return err
}

func (m *CLIMacOSDefaultsManager) RestartProcesses(ctx context.Context, processNames []string) error {
	if len(processNames) == 0 {
		return nil
	}
	if m.killallPath == "" {
		return fmt.Errorf("killall command not found")
	}

	for _, processName := range processNames {
		name := strings.TrimSpace(processName)
		if name == "" {
			continue
		}
		if strings.ContainsAny(name, "\x00\r\n/") {
			return fmt.Errorf("restart process name %q is invalid", name)
		}
		_, _ = m.run(ctx, m.killallPath, name)
	}
	return nil
}

func (m *CLIMacOSDefaultsManager) defaultsArgs(currentHost bool, args ...string) []string {
	if !currentHost {
		return args
	}
	return append([]string{"-currentHost"}, args...)
}

func runMacOSCommand(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s failed: %w\n%s", command, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func macOSDefaultPlanReady(model MacOSDefaultResourceModel) bool {
	return macOSSettingDomainReady(model.Domain) &&
		!model.Key.IsNull() && !model.Key.IsUnknown() &&
		!model.CurrentHost.IsNull() && !model.CurrentHost.IsUnknown() &&
		!model.DeleteOnDestroy.IsNull() && !model.DeleteOnDestroy.IsUnknown() &&
		!model.Value.IsUnknown() &&
		!model.Value.IsUnderlyingValueUnknown() &&
		!model.Restart.IsUnknown()
}

func macOSSettingDomainReady(value types.Object) bool {
	if value.IsNull() || value.IsUnknown() {
		return false
	}
	for _, attrValue := range value.Attributes() {
		if attrValue.IsUnknown() {
			return false
		}
	}
	return true
}

func macOSDefaultSpecFromModel(ctx context.Context, model MacOSDefaultResourceModel) (macOSDefaultSpec, diag.Diagnostics) {
	var diags diag.Diagnostics

	domain, domainDiags := macOSSettingDomainFromObject(ctx, model.Domain)
	diags.Append(domainDiags...)
	key := strings.TrimSpace(model.Key.ValueString())
	if key == "" {
		diags.AddError("Invalid macOS setting", "key must be non-empty")
	}
	if strings.ContainsAny(key, "\x00\r\n") {
		diags.AddError("Invalid macOS setting", "key must not contain control characters")
	}

	value, valueDiags := macOSDefaultValueFromModel(ctx, model)
	diags.Append(valueDiags...)
	restart, restartDiags := stringListValue(ctx, model.Restart, "restart")
	diags.Append(restartDiags...)
	if diags.HasError() {
		return macOSDefaultSpec{}, diags
	}

	currentHost := model.CurrentHost.ValueBool()
	if model.Restart.IsNull() {
		restart = defaultMacOSDefaultRestartProcesses(domain)
	}
	return macOSDefaultSpec{
		ID:              macOSDefaultID(domain, key, currentHost),
		Domain:          domain,
		Key:             key,
		CurrentHost:     currentHost,
		DeleteOnDestroy: model.DeleteOnDestroy.ValueBool(),
		Restart:         restart,
		Value:           value,
	}, diags
}

func macOSDefaultValueFromModel(ctx context.Context, model MacOSDefaultResourceModel) (macOSDefaultValue, diag.Diagnostics) {
	return macOSDefaultValueFromDynamic(ctx, model.Value)
}

func macOSDefaultValueFromDynamic(ctx context.Context, value types.Dynamic) (macOSDefaultValue, diag.Diagnostics) {
	var diags diag.Diagnostics

	if value.IsNull() || value.IsUnknown() || value.IsUnderlyingValueNull() || value.IsUnderlyingValueUnknown() {
		diags.AddError("Invalid macOS setting value", "value must be a known non-null bool, number, string, or list of strings.")
		return macOSDefaultValue{}, diags
	}

	switch underlying := value.UnderlyingValue().(type) {
	case types.Bool:
		if underlying.IsNull() || underlying.IsUnknown() {
			diags.AddError("Invalid macOS setting value", "value must be a known non-null bool.")
			return macOSDefaultValue{}, diags
		}
		return macOSDefaultValue{Type: macOSDefaultValueBool, Bool: underlying.ValueBool()}, diags
	case types.Number:
		parsed, err := macOSDefaultValueFromNumber(underlying)
		if err != nil {
			diags.AddError("Invalid macOS setting value", err.Error())
			return macOSDefaultValue{}, diags
		}
		return parsed, diags
	case types.Int64:
		if underlying.IsNull() || underlying.IsUnknown() {
			diags.AddError("Invalid macOS setting value", "value must be a known non-null number.")
			return macOSDefaultValue{}, diags
		}
		return macOSDefaultValue{Type: macOSDefaultValueInt, Int: underlying.ValueInt64()}, diags
	case types.Float64:
		if underlying.IsNull() || underlying.IsUnknown() {
			diags.AddError("Invalid macOS setting value", "value must be a known non-null number.")
			return macOSDefaultValue{}, diags
		}
		return macOSDefaultValue{Type: macOSDefaultValueFloat, Float: underlying.ValueFloat64()}, diags
	case types.String:
		if underlying.IsNull() || underlying.IsUnknown() {
			diags.AddError("Invalid macOS setting value", "value must be a known non-null string.")
			return macOSDefaultValue{}, diags
		}
		return macOSDefaultValue{Type: macOSDefaultValueString, String: underlying.ValueString()}, diags
	case types.List:
		elements, err := macOSDefaultStringSliceFromElements(underlying.Elements())
		if err != nil {
			diags.AddError("Invalid macOS setting value", err.Error())
			return macOSDefaultValue{}, diags
		}
		return macOSDefaultValue{Type: macOSDefaultValueStringList, StringList: elements}, diags
	case types.Tuple:
		elements, err := macOSDefaultStringSliceFromElements(underlying.Elements())
		if err != nil {
			diags.AddError("Invalid macOS setting value", err.Error())
			return macOSDefaultValue{}, diags
		}
		return macOSDefaultValue{Type: macOSDefaultValueStringList, StringList: elements}, diags
	default:
		diags.AddError(
			"Invalid macOS setting value",
			fmt.Sprintf("value has unsupported type %T; supported values are bool, number, string, and a list or tuple of strings.", underlying),
		)
		return macOSDefaultValue{}, diags
	}
}

func macOSDefaultModelWithValue(ctx context.Context, model MacOSDefaultResourceModel, value macOSDefaultValue) (MacOSDefaultResourceModel, error) {
	domain, diags := macOSSettingDomainFromObject(ctx, model.Domain)
	if diags.HasError() {
		return model, diagnosticsError(diags)
	}
	model.ID = types.StringValue(macOSDefaultID(domain, model.Key.ValueString(), model.CurrentHost.ValueBool()))
	model.DomainResolved = types.StringValue(domain)

	if !model.Value.IsNull() && !model.Value.IsUnknown() && !model.Value.IsUnderlyingValueNull() && !model.Value.IsUnderlyingValueUnknown() {
		configured, valueDiags := macOSDefaultValueFromDynamic(ctx, model.Value)
		if !valueDiags.HasError() && macOSDefaultValuesEqual(configured, value) {
			return model, nil
		}
	}

	dynamic, err := macOSDefaultDynamicValue(ctx, value)
	if err != nil {
		return model, err
	}
	model.Value = dynamic
	return model, nil
}

func macOSDefaultValueFromNumber(value types.Number) (macOSDefaultValue, error) {
	if value.IsNull() || value.IsUnknown() {
		return macOSDefaultValue{}, fmt.Errorf("value must be a known non-null number")
	}

	number := value.ValueBigFloat()
	if number == nil {
		return macOSDefaultValue{}, fmt.Errorf("value must be a known non-null number")
	}
	if integer, accuracy := new(big.Float).Copy(number).Int64(); accuracy == big.Exact {
		return macOSDefaultValue{Type: macOSDefaultValueInt, Int: integer}, nil
	}
	float, _ := number.Float64()
	return macOSDefaultValue{Type: macOSDefaultValueFloat, Float: float}, nil
}

func macOSDefaultStringSliceFromElements(elements []attr.Value) ([]string, error) {
	result := make([]string, 0, len(elements))
	for i, element := range elements {
		stringValue, ok := element.(types.String)
		if !ok {
			return nil, fmt.Errorf("string list element %d has unsupported type %T", i, element)
		}
		if stringValue.IsNull() || stringValue.IsUnknown() {
			return nil, fmt.Errorf("string list element %d must be known and non-null", i)
		}
		result = append(result, stringValue.ValueString())
	}
	return result, nil
}

func macOSDefaultDynamicValue(ctx context.Context, value macOSDefaultValue) (types.Dynamic, error) {
	switch value.Type {
	case macOSDefaultValueBool:
		return types.DynamicValue(types.BoolValue(value.Bool)), nil
	case macOSDefaultValueInt:
		return types.DynamicValue(types.NumberValue(new(big.Float).SetInt64(value.Int))), nil
	case macOSDefaultValueFloat:
		return types.DynamicValue(types.NumberValue(big.NewFloat(value.Float))), nil
	case macOSDefaultValueString:
		return types.DynamicValue(types.StringValue(value.String)), nil
	case macOSDefaultValueStringList:
		elementTypes := make([]attr.Type, 0, len(value.StringList))
		elements := make([]attr.Value, 0, len(value.StringList))
		for _, element := range value.StringList {
			elementTypes = append(elementTypes, types.StringType)
			elements = append(elements, types.StringValue(element))
		}
		tuple, diags := types.TupleValue(elementTypes, elements)
		if diags.HasError() {
			return types.Dynamic{}, diagnosticsError(diags)
		}
		return types.DynamicValue(tuple), nil
	default:
		return types.Dynamic{}, fmt.Errorf("unsupported macOS default value type %q", value.Type)
	}
}

func macOSDefaultValuesEqual(a macOSDefaultValue, b macOSDefaultValue) bool {
	return a.Type == b.Type &&
		a.Bool == b.Bool &&
		a.Int == b.Int &&
		a.Float == b.Float &&
		a.String == b.String &&
		reflect.DeepEqual(a.StringList, b.StringList)
}

func macOSDefaultImportSpec(importID string) (macOSDefaultSpec, error) {
	raw := strings.TrimSpace(importID)
	if raw == "" {
		return macOSDefaultSpec{}, fmt.Errorf("import ID must be non-empty")
	}

	currentHost := false
	remainder := raw
	first, rest, hasScope := strings.Cut(raw, ":")
	if hasScope && (first == "user" || first == "currentHost") {
		currentHost = first == "currentHost"
		remainder = rest
	}

	domain, key, ok := strings.Cut(remainder, ":")
	if !ok || strings.TrimSpace(domain) == "" || strings.TrimSpace(key) == "" {
		return macOSDefaultSpec{}, fmt.Errorf("expected import ID in the form \"domain:key\", \"user:domain:key\", or \"currentHost:domain:key\"")
	}
	domain = strings.TrimSpace(domain)
	key = strings.TrimSpace(key)
	if strings.ContainsAny(domain, "\x00\r\n") || strings.ContainsAny(key, "\x00\r\n") {
		return macOSDefaultSpec{}, fmt.Errorf("import ID must not contain control characters")
	}

	return macOSDefaultSpec{
		ID:              macOSDefaultID(domain, key, currentHost),
		Domain:          domain,
		Key:             key,
		CurrentHost:     currentHost,
		DeleteOnDestroy: false,
		Restart:         defaultMacOSDefaultRestartProcesses(domain),
	}, nil
}

func macOSDefaultID(domain string, key string, currentHost bool) string {
	scope := "user"
	if currentHost {
		scope = "currentHost"
	}
	return scope + ":" + domain + ":" + key
}

func defaultMacOSDefaultRestartProcesses(domain string) []string {
	switch {
	case domain == "com.apple.dock":
		return []string{"Dock"}
	case domain == "com.apple.finder":
		return []string{"Finder"}
	case domain == "com.apple.systemuiserver" || strings.HasPrefix(domain, "com.apple.menuextra."):
		return []string{"SystemUIServer"}
	case domain == "NSGlobalDomain",
		domain == "com.apple.AppleMultitouchTrackpad",
		domain == "com.apple.driver.AppleBluetoothMultitouch.trackpad",
		domain == "com.apple.universalaccess":
		return []string{"cfprefsd"}
	default:
		return nil
	}
}

func macOSDefaultWriteArgs(value macOSDefaultValue) []string {
	switch value.Type {
	case macOSDefaultValueBool:
		if value.Bool {
			return []string{"-bool", "true"}
		}
		return []string{"-bool", "false"}
	case macOSDefaultValueInt:
		return []string{"-int", strconv.FormatInt(value.Int, 10)}
	case macOSDefaultValueFloat:
		return []string{"-float", strconv.FormatFloat(value.Float, 'f', -1, 64)}
	case macOSDefaultValueString:
		return []string{"-string", value.String}
	case macOSDefaultValueStringList:
		return append([]string{"-array"}, value.StringList...)
	default:
		return nil
	}
}

func parseMacOSDefaultsReadType(output string) (string, error) {
	value := strings.TrimSpace(output)
	switch value {
	case "Type is boolean":
		return macOSDefaultValueBool, nil
	case "Type is integer":
		return macOSDefaultValueInt, nil
	case "Type is float":
		return macOSDefaultValueFloat, nil
	case "Type is string":
		return macOSDefaultValueString, nil
	case "Type is array":
		return macOSDefaultValueStringList, nil
	default:
		return "", fmt.Errorf("unsupported defaults value type %q", value)
	}
}

func parseMacOSDefaultReadValue(valueType string, output string) (macOSDefaultValue, error) {
	value := strings.TrimSpace(output)
	switch valueType {
	case macOSDefaultValueBool:
		switch strings.ToLower(value) {
		case "1", "true", "yes":
			return macOSDefaultValue{Type: valueType, Bool: true}, nil
		case "0", "false", "no":
			return macOSDefaultValue{Type: valueType, Bool: false}, nil
		default:
			return macOSDefaultValue{}, fmt.Errorf("cannot parse boolean defaults value %q", value)
		}
	case macOSDefaultValueInt:
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return macOSDefaultValue{}, fmt.Errorf("cannot parse integer defaults value %q: %w", value, err)
		}
		return macOSDefaultValue{Type: valueType, Int: parsed}, nil
	case macOSDefaultValueFloat:
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return macOSDefaultValue{}, fmt.Errorf("cannot parse float defaults value %q: %w", value, err)
		}
		return macOSDefaultValue{Type: valueType, Float: parsed}, nil
	case macOSDefaultValueString:
		return macOSDefaultValue{Type: valueType, String: value}, nil
	case macOSDefaultValueStringList:
		elements, err := parseMacOSDefaultStringArray(output)
		if err != nil {
			return macOSDefaultValue{}, err
		}
		return macOSDefaultValue{Type: valueType, StringList: elements}, nil
	default:
		return macOSDefaultValue{}, fmt.Errorf("unsupported defaults value type %q", valueType)
	}
}

func parseMacOSDefaultStringArray(output string) ([]string, error) {
	value := strings.TrimSpace(output)
	if value == "" || value == "()" {
		return []string{}, nil
	}
	if !strings.HasPrefix(value, "(") || !strings.HasSuffix(value, ")") {
		return nil, fmt.Errorf("cannot parse defaults array value %q", value)
	}

	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "("), ")"))
	if body == "" {
		return []string{}, nil
	}

	var elements []string
	for _, rawLine := range strings.Split(body, "\n") {
		line := strings.TrimSpace(rawLine)
		line = strings.TrimSuffix(line, ",")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "\"") {
			unquoted, err := strconv.Unquote(line)
			if err != nil {
				return nil, fmt.Errorf("cannot parse defaults array element %q: %w", line, err)
			}
			elements = append(elements, unquoted)
			continue
		}
		elements = append(elements, line)
	}
	return elements, nil
}

func diagnosticsError(diags diag.Diagnostics) error {
	var messages []string
	for _, item := range diags {
		messages = append(messages, item.Summary()+": "+item.Detail())
	}
	return errors.New(strings.Join(messages, "\n"))
}
