package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var (
	_ resource.Resource                = &MacOSAudioMultiOutputResource{}
	_ resource.ResourceWithConfigure   = &MacOSAudioMultiOutputResource{}
	_ resource.ResourceWithImportState = &MacOSAudioMultiOutputResource{}
	_ resource.ResourceWithModifyPlan  = &MacOSAudioMultiOutputResource{}
)

type MacOSAudioMultiOutputResource struct {
	manager MacOSAudioManager
}

type MacOSAudioMultiOutputResourceModel struct {
	ID              types.String `tfsdk:"id"`
	UID             types.String `tfsdk:"uid"`
	Name            types.String `tfsdk:"name"`
	PrimaryDevice   types.Object `tfsdk:"primary_device"`
	Devices         types.List   `tfsdk:"devices"`
	SampleRateHz    types.Int64  `tfsdk:"sample_rate_hz"`
	DefaultOutput   types.Bool   `tfsdk:"default_output"`
	SystemOutput    types.Bool   `tfsdk:"system_output"`
	DeleteOnDestroy types.Bool   `tfsdk:"delete_on_destroy"`
}

type MacOSAudioDeviceSelectorModel struct {
	UID           types.String `tfsdk:"uid"`
	Name          types.String `tfsdk:"name"`
	BuiltinOutput types.String `tfsdk:"builtin_output"`
	ResolvedUID   types.String `tfsdk:"resolved_uid"`
	ResolvedName  types.String `tfsdk:"resolved_name"`
}

type MacOSAudioMultiOutputDeviceModel struct {
	UID             types.String `tfsdk:"uid"`
	Name            types.String `tfsdk:"name"`
	BuiltinOutput   types.String `tfsdk:"builtin_output"`
	DriftCorrection types.Bool   `tfsdk:"drift_correction"`
	ResolvedUID     types.String `tfsdk:"resolved_uid"`
	ResolvedName    types.String `tfsdk:"resolved_name"`
}

type resolvedMacOSAudioDevice struct {
	UID  string
	Name string
}

func NewMacOSAudioMultiOutputResource() resource.Resource {
	return &MacOSAudioMultiOutputResource{}
}

func (r *MacOSAudioMultiOutputResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mac_audio_multi_output"
}

func (r *MacOSAudioMultiOutputResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one macOS CoreAudio multi-output device.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "CoreAudio UID for the managed multi-output device.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"uid": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "CoreAudio UID for the multi-output device. Omit to adopt an existing device with the same name or generate a provider-owned UID.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Display name for the multi-output device.",
			},
			"primary_device": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Primary CoreAudio device used as the multi-output clock source.",
				Attributes:          macOSAudioDeviceSelectorSchemaAttributes(),
			},
			"devices": schema.ListNestedAttribute{
				Required:            true,
				MarkdownDescription: "Ordered output devices included in the multi-output device.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: macOSAudioMultiOutputDeviceSchemaAttributes(),
				},
			},
			"sample_rate_hz": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(48000),
				MarkdownDescription: "Nominal sample rate for the multi-output device. Defaults to 48000.",
			},
			"default_output": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Set this multi-output device as the current default output device.",
			},
			"system_output": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Set this multi-output device as the current system output device.",
			},
			"delete_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Destroy the CoreAudio multi-output device on Terraform destroy. Defaults to false.",
			},
		},
	}
}

func macOSAudioDeviceSelectorSchemaAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"uid": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "CoreAudio device UID.",
		},
		"name": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "CoreAudio device display name. The provider requires exactly one matching device.",
		},
		"builtin_output": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Built-in output selector. Supported values are `headphones` and `speakers`.",
		},
		"resolved_uid": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Resolved CoreAudio device UID.",
		},
		"resolved_name": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Resolved CoreAudio device display name.",
		},
	}
}

func macOSAudioMultiOutputDeviceSchemaAttributes() map[string]schema.Attribute {
	attrs := macOSAudioDeviceSelectorSchemaAttributes()
	attrs["drift_correction"] = schema.BoolAttribute{
		Optional:            true,
		Computed:            true,
		Default:             booldefault.StaticBool(false),
		MarkdownDescription: "Enable CoreAudio drift correction for this subdevice.",
	}
	return attrs
}

func (r *MacOSAudioMultiOutputResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.MacOSAudioManager == nil {
			resp.Diagnostics.AddError("macOS audio unavailable", "`host_mac_audio_multi_output` requires the macOS `swift` command to access CoreAudio.")
			return
		}
		r.manager = data.MacOSAudioManager
	case MacOSAudioManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or MacOSAudioManager, got %T.", req.ProviderData),
		)
	}
}

func (r *MacOSAudioMultiOutputResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan MacOSAudioMultiOutputResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.UID.IsUnknown() && !plan.UID.IsNull() {
		plan.ID = plan.UID
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *MacOSAudioMultiOutputResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MacOSAudioMultiOutputResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.writeState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create macOS multi-output device", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSAudioMultiOutputResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MacOSAudioMultiOutputResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	actual, exists, err := r.manager.ReadMultiOutput(ctx, state.UID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read macOS multi-output device", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	next, err := r.refreshState(ctx, state, actual)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read macOS multi-output device", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *MacOSAudioMultiOutputResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MacOSAudioMultiOutputResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.writeState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to update macOS multi-output device", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSAudioMultiOutputResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MacOSAudioMultiOutputResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.DeleteOnDestroy.ValueBool() {
		if err := r.manager.DeleteMultiOutput(ctx, state.UID.ValueString()); err != nil {
			resp.Diagnostics.AddError("Failed to delete macOS multi-output device", err.Error())
		}
	}
}

func (r *MacOSAudioMultiOutputResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	actual, exists, err := r.manager.ReadMultiOutput(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import macOS multi-output device", err.Error())
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Failed to import macOS multi-output device", fmt.Sprintf("No multi-output device exists with UID %q.", req.ID))
		return
	}

	state, err := macOSAudioMultiOutputImportedModel(ctx, actual)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import macOS multi-output device", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSAudioMultiOutputResource) writeState(ctx context.Context, model MacOSAudioMultiOutputResourceModel) (MacOSAudioMultiOutputResourceModel, error) {
	if r.manager == nil {
		return model, fmt.Errorf("macOS audio manager unavailable")
	}

	spec, state, err := r.specFromModel(ctx, model, true)
	if err != nil {
		return model, err
	}
	actual, err := r.manager.WriteMultiOutput(ctx, spec)
	if err != nil {
		return model, err
	}
	return r.refreshState(ctx, state, actual)
}

func (r *MacOSAudioMultiOutputResource) refreshState(ctx context.Context, model MacOSAudioMultiOutputResourceModel, actual MacOSAudioMultiOutputSpec) (MacOSAudioMultiOutputResourceModel, error) {
	_, state, err := r.specFromModel(ctx, model, false)
	if err != nil {
		return model, err
	}
	state.ID = types.StringValue(actual.UID)
	state.UID = types.StringValue(actual.UID)
	state.Name = types.StringValue(actual.Name)
	if actual.SampleRateHz > 0 {
		state.SampleRateHz = types.Int64Value(actual.SampleRateHz)
	}
	state.DefaultOutput = types.BoolValue(actual.DefaultOutput)
	state.SystemOutput = types.BoolValue(actual.SystemOutput)
	return state, nil
}

func (r *MacOSAudioMultiOutputResource) specFromModel(ctx context.Context, model MacOSAudioMultiOutputResourceModel, adoptExisting bool) (MacOSAudioMultiOutputSpec, MacOSAudioMultiOutputResourceModel, error) {
	devices, err := r.manager.ListDevices(ctx)
	if err != nil {
		return MacOSAudioMultiOutputSpec{}, model, err
	}

	primaryModel, err := macOSAudioDeviceSelectorFromObject(ctx, model.PrimaryDevice)
	if err != nil {
		return MacOSAudioMultiOutputSpec{}, model, err
	}
	primary, err := resolveMacOSAudioDevice(devices, primaryModel)
	if err != nil {
		return MacOSAudioMultiOutputSpec{}, model, fmt.Errorf("primary_device: %w", err)
	}
	primaryModel.ResolvedUID = types.StringValue(primary.UID)
	primaryModel.ResolvedName = types.StringValue(primary.Name)
	primaryObject, err := macOSAudioDeviceSelectorObject(primaryModel)
	if err != nil {
		return MacOSAudioMultiOutputSpec{}, model, err
	}

	deviceModels, diags := macOSAudioMultiOutputDevicesFromList(ctx, model.Devices)
	if diags.HasError() {
		return MacOSAudioMultiOutputSpec{}, model, diagnosticsError(diags)
	}
	if len(deviceModels) < 2 {
		return MacOSAudioMultiOutputSpec{}, model, fmt.Errorf("devices must include at least two output devices")
	}

	specDevices := make([]MacOSAudioMultiOutputDeviceSpec, 0, len(deviceModels))
	for i := range deviceModels {
		resolved, err := resolveMacOSAudioDevice(devices, MacOSAudioDeviceSelectorModel{
			UID:           deviceModels[i].UID,
			Name:          deviceModels[i].Name,
			BuiltinOutput: deviceModels[i].BuiltinOutput,
		})
		if err != nil {
			return MacOSAudioMultiOutputSpec{}, model, fmt.Errorf("devices[%d]: %w", i, err)
		}
		deviceModels[i].ResolvedUID = types.StringValue(resolved.UID)
		deviceModels[i].ResolvedName = types.StringValue(resolved.Name)
		specDevices = append(specDevices, MacOSAudioMultiOutputDeviceSpec{
			UID:             resolved.UID,
			DriftCorrection: deviceModels[i].DriftCorrection.ValueBool(),
		})
	}
	deviceList, err := macOSAudioMultiOutputDevicesList(deviceModels)
	if err != nil {
		return MacOSAudioMultiOutputSpec{}, model, err
	}

	uid := model.UID.ValueString()
	if uid == "" {
		uid = macOSAudioMultiOutputGeneratedUID(model.Name.ValueString())
	}
	if adoptExisting && strings.HasPrefix(uid, macOSAudioMultiOutputGeneratedUIDPrefix) {
		if existingUID, ok, err := r.existingMultiOutputUIDByName(ctx, model.Name.ValueString()); err != nil {
			return MacOSAudioMultiOutputSpec{}, model, err
		} else if ok {
			uid = existingUID
		}
	}

	state := model
	state.ID = types.StringValue(uid)
	state.UID = types.StringValue(uid)
	state.PrimaryDevice = primaryObject
	state.Devices = deviceList

	return MacOSAudioMultiOutputSpec{
		UID:              uid,
		Name:             model.Name.ValueString(),
		PrimaryDeviceUID: primary.UID,
		Devices:          specDevices,
		SampleRateHz:     model.SampleRateHz.ValueInt64(),
		DefaultOutput:    model.DefaultOutput.ValueBool(),
		SystemOutput:     model.SystemOutput.ValueBool(),
	}, state, nil
}

func (r *MacOSAudioMultiOutputResource) existingMultiOutputUIDByName(ctx context.Context, name string) (string, bool, error) {
	devices, err := r.manager.ListDevices(ctx)
	if err != nil {
		return "", false, err
	}

	var matches []string
	for _, device := range devices {
		if device.Name != name {
			continue
		}
		if _, exists, err := r.manager.ReadMultiOutput(ctx, device.UID); err != nil {
			return "", false, err
		} else if exists {
			matches = append(matches, device.UID)
		}
	}
	if len(matches) == 0 {
		return "", false, nil
	}
	if len(matches) > 1 {
		return "", false, fmt.Errorf("multiple existing multi-output devices are named %q; set uid explicitly", name)
	}
	return matches[0], true, nil
}

func macOSAudioDeviceSelectorFromObject(ctx context.Context, value types.Object) (MacOSAudioDeviceSelectorModel, error) {
	var model MacOSAudioDeviceSelectorModel
	diags := value.As(ctx, &model, basetypesObjectAsOptions())
	if diags.HasError() {
		return model, diagnosticsError(diags)
	}
	return model, nil
}

func basetypesObjectAsOptions() basetypes.ObjectAsOptions {
	return basetypes.ObjectAsOptions{
		UnhandledNullAsEmpty:    true,
		UnhandledUnknownAsEmpty: true,
	}
}

func macOSAudioMultiOutputDevicesFromList(ctx context.Context, value types.List) ([]MacOSAudioMultiOutputDeviceModel, diag.Diagnostics) {
	var devices []MacOSAudioMultiOutputDeviceModel
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		diags.AddError("Invalid macOS audio devices", "devices must be known")
		return nil, diags
	}
	diags.Append(value.ElementsAs(ctx, &devices, false)...)
	return devices, diags
}

func resolveMacOSAudioDevice(devices []MacOSAudioDevice, selector MacOSAudioDeviceSelectorModel) (resolvedMacOSAudioDevice, error) {
	device, err := resolveMacOSAudioDeviceInfo(devices, selector)
	if err != nil {
		return resolvedMacOSAudioDevice{}, err
	}
	return resolvedMacOSAudioDevice{UID: device.UID, Name: device.Name}, nil
}

func resolveMacOSAudioDeviceInfo(devices []MacOSAudioDevice, selector MacOSAudioDeviceSelectorModel) (MacOSAudioDevice, error) {
	kind, value, err := macOSAudioDeviceSelectorValue(selector)
	if err != nil {
		return MacOSAudioDevice{}, err
	}

	switch kind {
	case "uid":
		return findMacOSAudioDeviceByUID(devices, value)
	case "name":
		return findMacOSAudioDeviceByName(devices, value)
	case "builtin_output":
		return findMacOSAudioBuiltinOutputDevice(devices, value)
	default:
		return MacOSAudioDevice{}, fmt.Errorf("unsupported selector %q", kind)
	}
}

func macOSAudioDeviceSelectorValue(selector MacOSAudioDeviceSelectorModel) (string, string, error) {
	values := []struct {
		kind  string
		value types.String
	}{
		{kind: "uid", value: selector.UID},
		{kind: "name", value: selector.Name},
		{kind: "builtin_output", value: selector.BuiltinOutput},
	}

	var kind string
	var raw string
	for _, candidate := range values {
		if candidate.value.IsNull() || candidate.value.IsUnknown() || candidate.value.ValueString() == "" {
			continue
		}
		if kind != "" {
			return "", "", fmt.Errorf("exactly one of uid, name, or builtin_output must be set")
		}
		kind = candidate.kind
		raw = candidate.value.ValueString()
	}
	if kind == "" {
		return "", "", fmt.Errorf("exactly one of uid, name, or builtin_output must be set")
	}
	return kind, raw, nil
}

func findMacOSAudioDeviceByUID(devices []MacOSAudioDevice, uid string) (MacOSAudioDevice, error) {
	for _, device := range devices {
		if device.UID == uid {
			return device, nil
		}
	}
	return MacOSAudioDevice{}, fmt.Errorf("no CoreAudio device exists with UID %q", uid)
}

func findMacOSAudioDeviceByName(devices []MacOSAudioDevice, name string) (MacOSAudioDevice, error) {
	var matches []MacOSAudioDevice
	for _, device := range devices {
		if device.Name == name {
			matches = append(matches, device)
		}
	}
	if len(matches) == 0 {
		return MacOSAudioDevice{}, fmt.Errorf("no CoreAudio device exists with name %q", name)
	}
	if len(matches) > 1 {
		return MacOSAudioDevice{}, fmt.Errorf("multiple CoreAudio devices are named %q; use uid instead", name)
	}
	return matches[0], nil
}

func findMacOSAudioBuiltinOutputDevice(devices []MacOSAudioDevice, selector string) (MacOSAudioDevice, error) {
	var uid string
	switch selector {
	case "headphones":
		uid = "BuiltInHeadphoneOutputDevice"
	case "speakers":
		uid = "BuiltInSpeakerDevice"
	default:
		return MacOSAudioDevice{}, fmt.Errorf("unsupported builtin_output %q; expected headphones or speakers", selector)
	}
	return findMacOSAudioDeviceByUID(devices, uid)
}

func macOSAudioDeviceSelectorAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"uid":            types.StringType,
		"name":           types.StringType,
		"builtin_output": types.StringType,
		"resolved_uid":   types.StringType,
		"resolved_name":  types.StringType,
	}
}

func macOSAudioMultiOutputDeviceAttributeTypes() map[string]attr.Type {
	attrTypes := macOSAudioDeviceSelectorAttributeTypes()
	attrTypes["drift_correction"] = types.BoolType
	return attrTypes
}

func macOSAudioDeviceSelectorObject(model MacOSAudioDeviceSelectorModel) (types.Object, error) {
	diags := diag.Diagnostics{}
	value, objectDiags := types.ObjectValue(macOSAudioDeviceSelectorAttributeTypes(), map[string]attr.Value{
		"uid":            nullStringIfUnset(model.UID),
		"name":           nullStringIfUnset(model.Name),
		"builtin_output": nullStringIfUnset(model.BuiltinOutput),
		"resolved_uid":   nullStringIfUnset(model.ResolvedUID),
		"resolved_name":  nullStringIfUnset(model.ResolvedName),
	})
	diags.Append(objectDiags...)
	if diags.HasError() {
		return types.Object{}, diagnosticsError(diags)
	}
	return value, nil
}

func macOSAudioMultiOutputDevicesList(models []MacOSAudioMultiOutputDeviceModel) (types.List, error) {
	diags := diag.Diagnostics{}
	elements := make([]attr.Value, 0, len(models))
	for _, model := range models {
		value, objectDiags := types.ObjectValue(macOSAudioMultiOutputDeviceAttributeTypes(), map[string]attr.Value{
			"uid":              nullStringIfUnset(model.UID),
			"name":             nullStringIfUnset(model.Name),
			"builtin_output":   nullStringIfUnset(model.BuiltinOutput),
			"drift_correction": nullBoolIfUnset(model.DriftCorrection),
			"resolved_uid":     nullStringIfUnset(model.ResolvedUID),
			"resolved_name":    nullStringIfUnset(model.ResolvedName),
		})
		diags.Append(objectDiags...)
		elements = append(elements, value)
	}
	list, listDiags := types.ListValue(types.ObjectType{AttrTypes: macOSAudioMultiOutputDeviceAttributeTypes()}, elements)
	diags.Append(listDiags...)
	if diags.HasError() {
		return types.List{}, diagnosticsError(diags)
	}
	return list, nil
}

func macOSAudioMultiOutputImportedModel(ctx context.Context, spec MacOSAudioMultiOutputSpec) (MacOSAudioMultiOutputResourceModel, error) {
	primary, err := macOSAudioDeviceSelectorObject(MacOSAudioDeviceSelectorModel{
		UID:         types.StringValue(spec.PrimaryDeviceUID),
		ResolvedUID: types.StringValue(spec.PrimaryDeviceUID),
	})
	if err != nil {
		return MacOSAudioMultiOutputResourceModel{}, err
	}

	devices := make([]MacOSAudioMultiOutputDeviceModel, 0, len(spec.Devices))
	for _, device := range spec.Devices {
		devices = append(devices, MacOSAudioMultiOutputDeviceModel{
			UID:             types.StringValue(device.UID),
			DriftCorrection: types.BoolValue(device.DriftCorrection),
			ResolvedUID:     types.StringValue(device.UID),
		})
	}
	deviceList, err := macOSAudioMultiOutputDevicesList(devices)
	if err != nil {
		return MacOSAudioMultiOutputResourceModel{}, err
	}

	return MacOSAudioMultiOutputResourceModel{
		ID:              types.StringValue(spec.UID),
		UID:             types.StringValue(spec.UID),
		Name:            types.StringValue(spec.Name),
		PrimaryDevice:   primary,
		Devices:         deviceList,
		SampleRateHz:    types.Int64Value(spec.SampleRateHz),
		DefaultOutput:   types.BoolValue(spec.DefaultOutput),
		SystemOutput:    types.BoolValue(spec.SystemOutput),
		DeleteOnDestroy: types.BoolValue(false),
	}, nil
}

func nullStringIfUnset(value types.String) types.String {
	if value.IsUnknown() || value.ValueString() == "" {
		return types.StringNull()
	}
	return value
}

func nullBoolIfUnset(value types.Bool) types.Bool {
	if value.IsUnknown() {
		return types.BoolNull()
	}
	return value
}

const macOSAudioMultiOutputGeneratedUIDPrefix = "terraform-provider-host.multi-output."

func macOSAudioMultiOutputGeneratedUID(name string) string {
	slug := strings.ToLower(name)
	slug = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "default"
	}
	return macOSAudioMultiOutputGeneratedUIDPrefix + slug
}
