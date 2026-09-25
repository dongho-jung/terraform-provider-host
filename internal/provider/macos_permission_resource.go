package provider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &MacOSPermissionResource{}
	_ resource.ResourceWithConfigure   = &MacOSPermissionResource{}
	_ resource.ResourceWithImportState = &MacOSPermissionResource{}
	_ resource.ResourceWithModifyPlan  = &MacOSPermissionResource{}
)

// macOS privacy permissions are granted by the user in System Settings. No
// supported interface grants one from a script, so this resource records the
// permission a configuration depends on, reports whether it is in place, and
// names the exact pane that grants it.
const (
	macOSPermissionStateGranted = "granted"
	macOSPermissionStateDenied  = "denied"
	macOSPermissionStateMissing = "missing"
	macOSPermissionStateUnknown = "unknown"
)

const (
	macOSPermissionClientBundleID = "bundle_id"
	macOSPermissionClientPath     = "path"
)

const macOSPermissionSettingsScheme = "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension"

// macOSPermissionService describes one TCC service: the identifier stored in
// the privacy database, whether that database is the system one or the
// per-user one, and the System Settings anchor that shows it.
type macOSPermissionService struct {
	Name     string
	Service  string
	System   bool
	Anchor   string
	PaneName string
}

var macOSPermissionServices = map[string]macOSPermissionService{
	"accessibility":      {Name: "accessibility", Service: "kTCCServiceAccessibility", System: true, Anchor: "Privacy_Accessibility", PaneName: "Accessibility"},
	"input_monitoring":   {Name: "input_monitoring", Service: "kTCCServiceListenEvent", System: true, Anchor: "Privacy_ListenEvent", PaneName: "Input Monitoring"},
	"screen_recording":   {Name: "screen_recording", Service: "kTCCServiceScreenCapture", System: true, Anchor: "Privacy_ScreenCapture", PaneName: "Screen & System Audio Recording"},
	"full_disk_access":   {Name: "full_disk_access", Service: "kTCCServiceSystemPolicyAllFiles", System: true, Anchor: "Privacy_AllFiles", PaneName: "Full Disk Access"},
	"developer_tools":    {Name: "developer_tools", Service: "kTCCServiceDeveloperTool", System: true, Anchor: "Privacy_DevTools", PaneName: "Developer Tools"},
	"bluetooth":          {Name: "bluetooth", Service: "kTCCServiceBluetoothAlways", System: true, Anchor: "Privacy_Bluetooth", PaneName: "Bluetooth"},
	"automation":         {Name: "automation", Service: "kTCCServiceAppleEvents", System: false, Anchor: "Privacy_Automation", PaneName: "Automation"},
	"microphone":         {Name: "microphone", Service: "kTCCServiceMicrophone", System: false, Anchor: "Privacy_Microphone", PaneName: "Microphone"},
	"camera":             {Name: "camera", Service: "kTCCServiceCamera", System: false, Anchor: "Privacy_Camera", PaneName: "Camera"},
	"contacts":           {Name: "contacts", Service: "kTCCServiceAddressBook", System: false, Anchor: "Privacy_Contacts", PaneName: "Contacts"},
	"calendars":          {Name: "calendars", Service: "kTCCServiceCalendar", System: false, Anchor: "Privacy_Calendars", PaneName: "Calendars"},
	"reminders":          {Name: "reminders", Service: "kTCCServiceReminders", System: false, Anchor: "Privacy_Reminders", PaneName: "Reminders"},
	"photos":             {Name: "photos", Service: "kTCCServicePhotos", System: false, Anchor: "Privacy_Photos", PaneName: "Photos"},
	"desktop_folder":     {Name: "desktop_folder", Service: "kTCCServiceSystemPolicyDesktopFolder", System: false, Anchor: "Privacy_DesktopFolder", PaneName: "Files & Folders"},
	"documents_folder":   {Name: "documents_folder", Service: "kTCCServiceSystemPolicyDocumentsFolder", System: false, Anchor: "Privacy_DocumentsFolder", PaneName: "Files & Folders"},
	"downloads_folder":   {Name: "downloads_folder", Service: "kTCCServiceSystemPolicyDownloadsFolder", System: false, Anchor: "Privacy_DownloadsFolder", PaneName: "Files & Folders"},
	"removable_volumes":  {Name: "removable_volumes", Service: "kTCCServiceSystemPolicyRemovableVolumes", System: false, Anchor: "Privacy_RemovableVolumes", PaneName: "Files & Folders"},
	"speech_recognition": {Name: "speech_recognition", Service: "kTCCServiceSpeechRecognition", System: false, Anchor: "Privacy_SpeechRecognition", PaneName: "Speech Recognition"},
	"media_library":      {Name: "media_library", Service: "kTCCServiceMediaLibrary", System: false, Anchor: "Privacy_Media", PaneName: "Media & Apple Music"},
}

func macOSPermissionServiceNames() []string {
	names := make([]string, 0, len(macOSPermissionServices))
	for name := range macOSPermissionServices {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// lookupMacOSPermissionService accepts the short name used in configuration or
// the raw `kTCCService…` identifier stored by macOS.
func lookupMacOSPermissionService(name string) (macOSPermissionService, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return macOSPermissionService{}, fmt.Errorf("service must be non-empty")
	}

	if service, ok := macOSPermissionServices[strings.ToLower(trimmed)]; ok {
		return service, nil
	}
	for _, service := range macOSPermissionServices {
		if strings.EqualFold(service.Service, trimmed) {
			return service, nil
		}
	}
	return macOSPermissionService{}, fmt.Errorf(
		"unknown privacy service %q; supported services are %s, or a raw kTCCService identifier",
		trimmed,
		strings.Join(macOSPermissionServiceNames(), ", "),
	)
}

// SettingsURL is the System Settings deep link that shows this service.
func (s macOSPermissionService) SettingsURL() string {
	return macOSPermissionSettingsScheme + "?" + s.Anchor
}

// ResetName is the service name that `tccutil reset` expects.
func (s macOSPermissionService) ResetName() string {
	return strings.TrimPrefix(s.Service, "kTCCService")
}

type macOSPermissionSpec struct {
	ID         string
	Service    macOSPermissionService
	Client     string
	ClientType string
}

type MacOSPermissionManager interface {
	PermissionState(ctx context.Context, spec macOSPermissionSpec) (string, error)
	RevokePermission(ctx context.Context, spec macOSPermissionSpec) error
	OpenSettings(ctx context.Context, settingsURL string) error
}

type CLIMacOSPermissionManager struct {
	sqlitePath  string
	tccutilPath string
	openPath    string
	homeDir     string
	run         macOSCommandRunner
}

func NewCLIMacOSPermissionManager(sqlitePath string, tccutilPath string, openPath string, homeDir string) MacOSPermissionManager {
	return &CLIMacOSPermissionManager{
		sqlitePath:  sqlitePath,
		tccutilPath: tccutilPath,
		openPath:    openPath,
		homeDir:     homeDir,
		run:         runMacOSCommand,
	}
}

type MacOSPermissionResource struct {
	manager MacOSPermissionManager
}

type MacOSPermissionResourceModel struct {
	ID                      types.String `tfsdk:"id"`
	Service                 types.String `tfsdk:"service"`
	Client                  types.String `tfsdk:"client"`
	ClientType              types.String `tfsdk:"client_type"`
	Required                types.Bool   `tfsdk:"required"`
	OpenSettingsWhenMissing types.Bool   `tfsdk:"open_settings_when_missing"`
	RevokeOnDestroy         types.Bool   `tfsdk:"revoke_on_destroy"`
	State                   types.String `tfsdk:"state"`
	Granted                 types.Bool   `tfsdk:"granted"`
	SettingsURL             types.String `tfsdk:"settings_url"`
}

func NewMacOSPermissionResource() resource.Resource {
	return &MacOSPermissionResource{}
}

func (r *MacOSPermissionResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mac_permission"
}

func (r *MacOSPermissionResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Declares a macOS privacy permission an application needs, and verifies whether it is granted.\n\n" +
			"macOS only grants Privacy & Security permissions through an explicit user action in System Settings or through a user-approved MDM profile. " +
			"No supported interface grants one from a script, so this resource never changes a grant. It records the dependency, reports the current state when the privacy database is readable, and names the pane that grants it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier derived from `service` and `client`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"service": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Privacy service to check. Use a short name such as `accessibility`, `input_monitoring`, `screen_recording`, or `full_disk_access`, or a raw `kTCCService…` identifier. Supported short names: `" +
					strings.Join(macOSPermissionServiceNames(), "`, `") + "`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"client": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Bundle identifier such as `org.hammerspoon.Hammerspoon`, or the absolute path of a command-line executable.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"client_type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "How macOS identifies the client, either `bundle_id` or `path`, derived from `client`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"required": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Fail the apply when the permission is confirmed missing or denied. A state that cannot be read is always reported as a warning, never an error. Defaults to true.",
			},
			"open_settings_when_missing": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Open the System Settings pane that grants this permission whenever it is not confirmed granted. Defaults to false.",
			},
			"revoke_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Run `tccutil reset` for this service and client on destroy, returning the permission to its undecided state. Requires a bundle identifier `client`. Defaults to false.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"state": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Observed permission state: `granted`, `denied`, `missing` when macOS has no decision recorded, or `unknown` when the privacy database cannot be read.",
			},
			"granted": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "True only when the permission was read and is granted.",
			},
			"settings_url": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "System Settings URL that opens the pane granting this permission.",
			},
		},
	}
}

func (r *MacOSPermissionResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if !requireHostUserScope(data, "host_mac_permission", &resp.Diagnostics) {
			return
		}
		if data.MacOSPermissionManager == nil {
			resp.Diagnostics.AddError("macOS permissions unavailable", "`host_mac_permission` requires macOS.")
			return
		}
		r.manager = data.MacOSPermissionManager
	case MacOSPermissionManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or MacOSPermissionManager, got %T.", req.ProviderData),
		)
	}
}

func (r *MacOSPermissionResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan MacOSPermissionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Service.IsUnknown() || plan.Client.IsUnknown() {
		return
	}

	spec, diags := macOSPermissionSpecFromModel(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = types.StringValue(spec.ID)
	plan.ClientType = types.StringValue(spec.ClientType)
	plan.SettingsURL = types.StringValue(spec.Service.SettingsURL())
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *MacOSPermissionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MacOSPermissionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state := r.verify(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSPermissionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MacOSPermissionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A refresh only records what macOS reports. Failing an apply is the job of
	// Create and Update, where the configuration is known.
	var refreshDiags diag.Diagnostics
	next := r.verify(ctx, state, &refreshDiags)
	for _, item := range refreshDiags {
		if item.Severity() == diag.SeverityError {
			resp.Diagnostics.AddWarning(item.Summary(), item.Detail())
			continue
		}
		resp.Diagnostics.Append(item)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *MacOSPermissionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MacOSPermissionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state := r.verify(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSPermissionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MacOSPermissionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !state.RevokeOnDestroy.ValueBool() {
		return
	}

	spec, diags := macOSPermissionSpecFromModel(state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if spec.ClientType != macOSPermissionClientBundleID {
		resp.Diagnostics.AddError(
			"Cannot revoke macOS permission",
			"revoke_on_destroy uses `tccutil reset`, which only accepts a bundle identifier. Set revoke_on_destroy to false for a path client.",
		)
		return
	}
	if r.manager == nil {
		resp.Diagnostics.AddError("macOS permissions unavailable", "`host_mac_permission` requires macOS.")
		return
	}

	if err := r.manager.RevokePermission(ctx, spec); err != nil {
		resp.Diagnostics.AddError("Failed to revoke macOS permission", err.Error())
	}
}

func (r *MacOSPermissionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	service, client, ok := strings.Cut(strings.TrimSpace(req.ID), ":")
	if !ok || strings.TrimSpace(service) == "" || strings.TrimSpace(client) == "" {
		resp.Diagnostics.AddError(
			"Failed to import macOS permission",
			"expected import ID in the form \"<service>:<client>\", such as \"accessibility:org.hammerspoon.Hammerspoon\"",
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service"), strings.TrimSpace(service))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("client"), strings.TrimSpace(client))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("required"), true)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("open_settings_when_missing"), false)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("revoke_on_destroy"), false)...)
}

// verify reads the current permission state and reports what the operator has
// to do when it is not in place.
func (r *MacOSPermissionResource) verify(ctx context.Context, model MacOSPermissionResourceModel, diags *diag.Diagnostics) MacOSPermissionResourceModel {
	spec, specDiags := macOSPermissionSpecFromModel(model)
	diags.Append(specDiags...)
	if specDiags.HasError() {
		return model
	}

	model.ID = types.StringValue(spec.ID)
	model.ClientType = types.StringValue(spec.ClientType)
	model.SettingsURL = types.StringValue(spec.Service.SettingsURL())

	if r.manager == nil {
		diags.AddError("macOS permissions unavailable", "`host_mac_permission` requires macOS.")
		return model
	}

	state, err := r.manager.PermissionState(ctx, spec)
	if err != nil {
		diags.AddError("Failed to read macOS permission", err.Error())
		return model
	}

	model.State = types.StringValue(state)
	model.Granted = types.BoolValue(state == macOSPermissionStateGranted)
	if state == macOSPermissionStateGranted {
		return model
	}

	if model.OpenSettingsWhenMissing.ValueBool() {
		if err := r.manager.OpenSettings(ctx, spec.Service.SettingsURL()); err != nil {
			diags.AddWarning("Failed to open System Settings", err.Error())
		}
	}

	summary, detail := macOSPermissionDiagnostic(spec, state)
	if state == macOSPermissionStateUnknown || !model.Required.ValueBool() {
		diags.AddWarning(summary, detail)
		return model
	}
	diags.AddError(summary, detail)
	return model
}

func macOSPermissionDiagnostic(spec macOSPermissionSpec, state string) (string, string) {
	grant := fmt.Sprintf(
		"Grant it in System Settings > Privacy & Security > %s, then re-run Terraform. The pane opens with:\n    open %q\n\nmacOS only accepts this grant from an explicit user action or a user-approved MDM profile, so Terraform cannot perform it.",
		spec.Service.PaneName,
		spec.Service.SettingsURL(),
	)

	switch state {
	case macOSPermissionStateUnknown:
		return fmt.Sprintf("Cannot verify macOS %s permission for %s", spec.Service.PaneName, spec.Client),
			fmt.Sprintf(
				"Reading the privacy database requires Full Disk Access for the application running Terraform, such as the terminal. Without it Terraform cannot tell whether %s already has this permission.\n\n%s",
				spec.Client,
				grant,
			)
	case macOSPermissionStateDenied:
		return fmt.Sprintf("macOS %s permission is denied for %s", spec.Service.PaneName, spec.Client),
			fmt.Sprintf("macOS has recorded an explicit denial for %s.\n\n%s", spec.Client, grant)
	default:
		return fmt.Sprintf("macOS %s permission is not granted to %s", spec.Service.PaneName, spec.Client),
			fmt.Sprintf("macOS has no decision recorded for %s.\n\n%s", spec.Client, grant)
	}
}

func macOSPermissionSpecFromModel(model MacOSPermissionResourceModel) (macOSPermissionSpec, diag.Diagnostics) {
	var diags diag.Diagnostics

	service, err := lookupMacOSPermissionService(model.Service.ValueString())
	if err != nil {
		diags.AddError("Invalid macOS permission", err.Error())
		return macOSPermissionSpec{}, diags
	}

	client := strings.TrimSpace(model.Client.ValueString())
	if client == "" {
		diags.AddError("Invalid macOS permission", "client must be non-empty")
		return macOSPermissionSpec{}, diags
	}
	if strings.ContainsAny(client, "\x00\r\n") {
		diags.AddError("Invalid macOS permission", "client must not contain control characters")
		return macOSPermissionSpec{}, diags
	}

	clientType := macOSPermissionClientBundleID
	if filepath.IsAbs(client) {
		clientType = macOSPermissionClientPath
	}

	return macOSPermissionSpec{
		ID:         service.Name + ":" + client,
		Service:    service,
		Client:     client,
		ClientType: clientType,
	}, diags
}

// PermissionState never fails. The privacy database is protected by the very
// mechanism it records, so a database this process may not read is a state the
// operator has to act on, not an error that should stop an apply.
func (m *CLIMacOSPermissionManager) PermissionState(ctx context.Context, spec macOSPermissionSpec) (string, error) {
	return m.permissionState(ctx, spec), nil
}

func (m *CLIMacOSPermissionManager) permissionState(ctx context.Context, spec macOSPermissionSpec) string {
	if m.sqlitePath == "" {
		return macOSPermissionStateUnknown
	}

	database := m.databasePath(spec.Service)
	if database == "" {
		return macOSPermissionStateUnknown
	}
	if _, statErr := os.Stat(database); statErr != nil {
		if os.IsNotExist(statErr) {
			return macOSPermissionStateMissing
		}
		return macOSPermissionStateUnknown
	}

	query := fmt.Sprintf(
		"SELECT auth_value FROM access WHERE service=%s AND client=%s ORDER BY auth_value DESC LIMIT 1;",
		sqliteQuote(spec.Service.Service),
		sqliteQuote(spec.Client),
	)
	out, runErr := m.run(ctx, m.sqlitePath, "-readonly", macOSPermissionDatabaseURI(database), query)
	if runErr != nil {
		return macOSPermissionStateUnknown
	}

	value := strings.TrimSpace(string(out))
	if value == "" {
		return macOSPermissionStateMissing
	}
	auth, parseErr := strconv.Atoi(value)
	if parseErr != nil {
		return macOSPermissionStateUnknown
	}
	switch auth {
	case 0:
		return macOSPermissionStateDenied
	case 1:
		return macOSPermissionStateMissing
	default:
		return macOSPermissionStateGranted
	}
}

func (m *CLIMacOSPermissionManager) RevokePermission(ctx context.Context, spec macOSPermissionSpec) error {
	if m.tccutilPath == "" {
		return fmt.Errorf("tccutil command not found")
	}
	_, err := m.run(ctx, m.tccutilPath, "reset", spec.Service.ResetName(), spec.Client)
	return err
}

func (m *CLIMacOSPermissionManager) OpenSettings(ctx context.Context, settingsURL string) error {
	if m.openPath == "" {
		return fmt.Errorf("open command not found")
	}
	_, err := m.run(ctx, m.openPath, settingsURL)
	return err
}

func (m *CLIMacOSPermissionManager) databasePath(service macOSPermissionService) string {
	if service.System {
		return "/Library/Application Support/com.apple.TCC/TCC.db"
	}
	if m.homeDir == "" {
		return ""
	}
	return filepath.Join(m.homeDir, "Library", "Application Support", "com.apple.TCC", "TCC.db")
}

// macOSPermissionDatabaseURI opens the privacy database immutably, so a
// read never waits for a lock the running system holds.
func macOSPermissionDatabaseURI(database string) string {
	uri := url.URL{Scheme: "file", Path: database, RawQuery: "immutable=1"}
	return uri.String()
}

func sqliteQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
