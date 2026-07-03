package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
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
	resp.TypeName = req.ProviderTypeName + "_macos_default"
}

func (r *MacOSDefaultResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one macOS `defaults` key with a typed value.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier derived from `domain`, `key`, and `current_host`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"domain": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Defaults domain, such as `com.apple.dock`, `NSGlobalDomain`, or `com.apple.universalaccess`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
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
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Delete the managed defaults key on destroy. Defaults to false, leaving the current host setting in place.",
			},
			"restart": schema.ListAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				MarkdownDescription: "Process names to restart with `killall` after writes or deletes, such as `Dock`, `Finder`, or `SystemUIServer`. Omit to use provider defaults for known domains; set `[]` to disable restarts.",
			},
		},
	}
}

func (r *MacOSDefaultResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.MacOSDefaultsManager == nil {
			resp.Diagnostics.AddError("macOS defaults unavailable", "`host_macos_default` requires the macOS `defaults` command.")
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
		resp.Diagnostics.AddError("Failed to sync macOS default", err.Error())
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
		resp.Diagnostics.AddError("Failed to read macOS default", err.Error())
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
		resp.Diagnostics.AddError("Failed to sync macOS default", err.Error())
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
			resp.Diagnostics.AddError("macOS defaults unavailable", "`host_macos_default` requires the macOS `defaults` command.")
			return
		}
		if err := r.manager.DeleteDefault(ctx, spec); err != nil {
			resp.Diagnostics.AddError("Failed to delete macOS default", err.Error())
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
		resp.Diagnostics.AddError("Failed to import macOS default", err.Error())
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

	state := MacOSDefaultResourceModel{
		ID:              types.StringValue(spec.ID),
		Domain:          types.StringValue(spec.Domain),
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
	return !model.Domain.IsNull() && !model.Domain.IsUnknown() &&
		!model.Key.IsNull() && !model.Key.IsUnknown() &&
		!model.CurrentHost.IsNull() && !model.CurrentHost.IsUnknown() &&
		!model.DeleteOnDestroy.IsNull() && !model.DeleteOnDestroy.IsUnknown() &&
		!model.Bool.IsUnknown() &&
		!model.Int.IsUnknown() &&
		!model.Float.IsUnknown() &&
		!model.String.IsUnknown() &&
		!model.StringList.IsUnknown() &&
		!model.Restart.IsUnknown()
}

func macOSDefaultSpecFromModel(ctx context.Context, model MacOSDefaultResourceModel) (macOSDefaultSpec, diag.Diagnostics) {
	var diags diag.Diagnostics

	domain := strings.TrimSpace(model.Domain.ValueString())
	key := strings.TrimSpace(model.Key.ValueString())
	if domain == "" {
		diags.AddError("Invalid macOS default", "domain must be non-empty")
	}
	if key == "" {
		diags.AddError("Invalid macOS default", "key must be non-empty")
	}
	if strings.ContainsAny(domain, "\x00\r\n") {
		diags.AddError("Invalid macOS default", "domain must not contain control characters")
	}
	if strings.ContainsAny(key, "\x00\r\n") {
		diags.AddError("Invalid macOS default", "key must not contain control characters")
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
	var diags diag.Diagnostics
	var values []macOSDefaultValue

	if !model.Bool.IsNull() {
		values = append(values, macOSDefaultValue{Type: macOSDefaultValueBool, Bool: model.Bool.ValueBool()})
	}
	if !model.Int.IsNull() {
		values = append(values, macOSDefaultValue{Type: macOSDefaultValueInt, Int: model.Int.ValueInt64()})
	}
	if !model.Float.IsNull() {
		values = append(values, macOSDefaultValue{Type: macOSDefaultValueFloat, Float: model.Float.ValueFloat64()})
	}
	if !model.String.IsNull() {
		values = append(values, macOSDefaultValue{Type: macOSDefaultValueString, String: model.String.ValueString()})
	}
	if !model.StringList.IsNull() {
		var elements []string
		diags.Append(model.StringList.ElementsAs(ctx, &elements, false)...)
		values = append(values, macOSDefaultValue{Type: macOSDefaultValueStringList, StringList: elements})
	}

	if len(values) != 1 {
		diags.AddError("Invalid macOS default value", "Exactly one of bool, int, float, string, or string_list must be set.")
		return macOSDefaultValue{}, diags
	}
	return values[0], diags
}

func macOSDefaultModelWithValue(ctx context.Context, model MacOSDefaultResourceModel, value macOSDefaultValue) (MacOSDefaultResourceModel, error) {
	model.ID = types.StringValue(macOSDefaultID(model.Domain.ValueString(), model.Key.ValueString(), model.CurrentHost.ValueBool()))
	model.Bool = types.BoolNull()
	model.Int = types.Int64Null()
	model.Float = types.Float64Null()
	model.String = types.StringNull()
	model.StringList = types.ListNull(types.StringType)

	switch value.Type {
	case macOSDefaultValueBool:
		model.Bool = types.BoolValue(value.Bool)
	case macOSDefaultValueInt:
		model.Int = types.Int64Value(value.Int)
	case macOSDefaultValueFloat:
		model.Float = types.Float64Value(value.Float)
	case macOSDefaultValueString:
		model.String = types.StringValue(value.String)
	case macOSDefaultValueStringList:
		list, diags := types.ListValueFrom(ctx, types.StringType, value.StringList)
		if diags.HasError() {
			return model, diagnosticsError(diags)
		}
		model.StringList = list
	default:
		return model, fmt.Errorf("unsupported macOS default value type %q", value.Type)
	}
	return model, nil
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
