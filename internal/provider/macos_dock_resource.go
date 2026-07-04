package provider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	tfpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &MacOSDockResource{}
	_ resource.ResourceWithConfigure   = &MacOSDockResource{}
	_ resource.ResourceWithImportState = &MacOSDockResource{}
	_ resource.ResourceWithModifyPlan  = &MacOSDockResource{}
)

const macOSDockResourceID = "com.apple.dock"

type MacOSDockResource struct {
	manager MacOSDockManager
	homeDir string
}

type MacOSDockResourceModel struct {
	ID              types.String `tfsdk:"id"`
	Apps            types.List   `tfsdk:"apps"`
	Folders         types.List   `tfsdk:"folders"`
	Restart         types.Bool   `tfsdk:"restart"`
	DeleteOnDestroy types.Bool   `tfsdk:"delete_on_destroy"`
}

type MacOSDockSpec struct {
	Apps            []string
	Folders         []string
	Restart         bool
	DeleteOnDestroy bool
}

type MacOSDockManager interface {
	ReadDock(ctx context.Context) (MacOSDockSpec, error)
	WriteDock(ctx context.Context, spec MacOSDockSpec) error
	RestartDock(ctx context.Context) error
}

type CLIMacOSDockManager struct {
	defaultsPath string
	killallPath  string
	run          macOSCommandRunner
}

func NewCLIMacOSDockManager(defaultsPath string, killallPath string) MacOSDockManager {
	return &CLIMacOSDockManager{
		defaultsPath: defaultsPath,
		killallPath:  killallPath,
		run:          runMacOSCommand,
	}
}

func NewMacOSDockResource() resource.Resource {
	return &MacOSDockResource{}
}

func (r *MacOSDockResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mac_dock"
}

func (r *MacOSDockResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the Dock persistent apps and folders as whole ordered lists.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, always `com.apple.dock`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"apps": schema.ListAttribute{
				ElementType:         types.StringType,
				Required:            true,
				MarkdownDescription: "Ordered `.app` bundle paths to show in the Dock persistent apps section. `~` is expanded to the provider `home_dir` and relative paths are resolved from the Terraform working directory.",
			},
			"folders": schema.ListAttribute{
				ElementType:         types.StringType,
				Required:            true,
				MarkdownDescription: "Ordered folder paths to show in the Dock persistent folders section. `~` is expanded to the provider `home_dir` and relative paths are resolved from the Terraform working directory. Set `[]` for none.",
			},
			"restart": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Restart Dock with `killall Dock` after writes or destructive deletes.",
			},
			"delete_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Clear the managed Dock app and folder arrays on destroy. Defaults to false, leaving the current Dock unchanged.",
			},
		},
	}
}

func (r *MacOSDockResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.MacOSDockManager == nil {
			resp.Diagnostics.AddError("macOS Dock unavailable", "`host_mac_dock` requires the macOS `defaults` command.")
			return
		}
		r.manager = data.MacOSDockManager
		r.homeDir = data.HomeDir
	case MacOSDockManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or MacOSDockManager, got %T.", req.ProviderData),
		)
	}
}

func (r *MacOSDockResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan MacOSDockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !macOSDockPlanReady(plan) {
		return
	}

	apps, appsKnown, appDiags := resolveMacOSDockListForPlanForHome(plan.Apps, "apps", true, r.homeDir)
	resp.Diagnostics.Append(appDiags...)
	folders, foldersKnown, folderDiags := resolveMacOSDockListForPlanForHome(plan.Folders, "folders", false, r.homeDir)
	resp.Diagnostics.Append(folderDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.Apps = apps
	plan.Folders = folders

	if appsKnown && foldersKnown {
		if _, diags := macOSDockSpecFromModelForHome(ctx, plan, r.homeDir); diags.HasError() {
			resp.Diagnostics.Append(diags...)
			return
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = types.StringValue(macOSDockResourceID)
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *MacOSDockResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MacOSDockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncDock(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync macOS Dock", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSDockResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MacOSDockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, err := r.readDock(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read macOS Dock", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *MacOSDockResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MacOSDockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncDock(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync macOS Dock", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSDockResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MacOSDockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.DeleteOnDestroy.IsNull() || state.DeleteOnDestroy.IsUnknown() || !state.DeleteOnDestroy.ValueBool() {
		return
	}
	if r.manager == nil {
		resp.Diagnostics.AddError("macOS Dock unavailable", "`host_mac_dock` requires the macOS `defaults` command.")
		return
	}

	restart := !state.Restart.IsNull() && !state.Restart.IsUnknown() && state.Restart.ValueBool()
	if err := r.manager.WriteDock(ctx, MacOSDockSpec{Restart: restart}); err != nil {
		resp.Diagnostics.AddError("Failed to clear macOS Dock", err.Error())
		return
	}
	if restart {
		if err := r.manager.RestartDock(ctx); err != nil {
			resp.Diagnostics.AddError("Failed to restart Dock", err.Error())
		}
	}
}

func (r *MacOSDockResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if strings.TrimSpace(req.ID) != "" && strings.TrimSpace(req.ID) != macOSDockResourceID {
		resp.Diagnostics.AddError("Invalid macOS Dock import ID", fmt.Sprintf("Expected %q.", macOSDockResourceID))
		return
	}
	if r.manager == nil {
		resp.Diagnostics.AddError("macOS Dock unavailable", "`host_mac_dock` requires the macOS `defaults` command.")
		return
	}

	spec, err := r.manager.ReadDock(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import macOS Dock", err.Error())
		return
	}
	state, err := macOSDockModelFromSpec(ctx, MacOSDockResourceModel{
		Restart:         types.BoolValue(true),
		DeleteOnDestroy: types.BoolValue(false),
	}, spec)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import macOS Dock", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, tfpath.Root("id"), types.StringValue(macOSDockResourceID))...)
}

func (r *MacOSDockResource) syncDock(ctx context.Context, model MacOSDockResourceModel) (MacOSDockResourceModel, error) {
	if r.manager == nil {
		return model, fmt.Errorf("macOS Dock manager unavailable")
	}

	spec, diags := macOSDockSpecFromModelForHome(ctx, model, r.homeDir)
	if diags.HasError() {
		return model, diagnosticsError(diags)
	}
	if err := r.manager.WriteDock(ctx, spec); err != nil {
		return model, err
	}
	if spec.Restart {
		if err := r.manager.RestartDock(ctx); err != nil {
			return model, err
		}
	}

	return macOSDockModelFromSpec(ctx, model, spec)
}

func (r *MacOSDockResource) readDock(ctx context.Context, model MacOSDockResourceModel) (MacOSDockResourceModel, error) {
	if r.manager == nil {
		return model, fmt.Errorf("macOS Dock manager unavailable")
	}

	spec, err := r.manager.ReadDock(ctx)
	if err != nil {
		return model, err
	}
	return macOSDockModelFromSpec(ctx, model, spec)
}

func (m *CLIMacOSDockManager) ReadDock(ctx context.Context) (MacOSDockSpec, error) {
	if m.defaultsPath == "" {
		return MacOSDockSpec{}, fmt.Errorf("defaults command not found")
	}

	appsOut, err := m.readDockArray(ctx, "persistent-apps")
	if err != nil {
		return MacOSDockSpec{}, err
	}
	foldersOut, err := m.readDockArray(ctx, "persistent-others")
	if err != nil {
		return MacOSDockSpec{}, err
	}

	return MacOSDockSpec{
		Apps:    parseMacOSDockFileURLs(string(appsOut)),
		Folders: parseMacOSDockFileURLs(string(foldersOut)),
	}, nil
}

func (m *CLIMacOSDockManager) WriteDock(ctx context.Context, spec MacOSDockSpec) error {
	if m.defaultsPath == "" {
		return fmt.Errorf("defaults command not found")
	}

	if err := m.writeDockArray(ctx, "persistent-apps", macOSDockEntries(spec.Apps, "file-tile")); err != nil {
		return err
	}
	if err := m.writeDockArray(ctx, "persistent-others", macOSDockEntries(spec.Folders, "directory-tile")); err != nil {
		return err
	}
	return nil
}

func (m *CLIMacOSDockManager) RestartDock(ctx context.Context) error {
	if m.killallPath == "" {
		return fmt.Errorf("killall command not found")
	}
	_, _ = m.run(ctx, m.killallPath, "Dock")
	return nil
}

func (m *CLIMacOSDockManager) readDockArray(ctx context.Context, key string) ([]byte, error) {
	out, err := m.run(ctx, m.defaultsPath, "read", macOSDockResourceID, key)
	if err != nil && isMacOSDefaultsMissingError(err) {
		return []byte("()"), nil
	}
	return out, err
}

func (m *CLIMacOSDockManager) writeDockArray(ctx context.Context, key string, entries []string) error {
	args := []string{"write", macOSDockResourceID, key, "-array"}
	args = append(args, entries...)
	_, err := m.run(ctx, m.defaultsPath, args...)
	return err
}

func macOSDockPlanReady(model MacOSDockResourceModel) bool {
	return !model.Apps.IsNull() && !model.Apps.IsUnknown() &&
		!model.Folders.IsNull() && !model.Folders.IsUnknown() &&
		!model.Restart.IsNull() && !model.Restart.IsUnknown() &&
		!model.DeleteOnDestroy.IsNull() && !model.DeleteOnDestroy.IsUnknown()
}

func resolveMacOSDockListForPlanForHome(value types.List, label string, wantApp bool, homeDir string) (types.List, bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	elements := make([]attr.Value, 0, len(value.Elements()))
	allKnown := true
	for _, element := range value.Elements() {
		if element.IsUnknown() {
			elements = append(elements, types.StringUnknown())
			allKnown = false
			continue
		}
		if element.IsNull() {
			diags.AddError("Invalid macOS Dock "+label, label+" entries must be non-null paths.")
			continue
		}
		stringValue, ok := element.(types.String)
		if !ok {
			diags.AddError("Invalid macOS Dock "+label, fmt.Sprintf("%s entries must be strings.", label))
			continue
		}
		resolved, err := resolveMacOSDockPathForHome(label, stringValue.ValueString(), wantApp, homeDir)
		if err != nil {
			diags.AddError("Invalid macOS Dock "+label, err.Error())
			continue
		}
		elements = append(elements, types.StringValue(resolved))
	}
	if diags.HasError() {
		return types.ListUnknown(types.StringType), false, diags
	}
	return types.ListValueMust(types.StringType, elements), allKnown, diags
}

func macOSDockSpecFromModelForHome(ctx context.Context, model MacOSDockResourceModel, homeDir string) (MacOSDockSpec, diag.Diagnostics) {
	var diags diag.Diagnostics
	apps, appDiags := stringListValue(ctx, model.Apps, "apps")
	diags.Append(appDiags...)
	folders, folderDiags := stringListValue(ctx, model.Folders, "folders")
	diags.Append(folderDiags...)
	if diags.HasError() {
		return MacOSDockSpec{}, diags
	}

	apps = resolveMacOSDockPathsForHome(&diags, "apps", apps, true, homeDir)
	folders = resolveMacOSDockPathsForHome(&diags, "folders", folders, false, homeDir)
	if diags.HasError() {
		return MacOSDockSpec{}, diags
	}

	return MacOSDockSpec{
		Apps:            apps,
		Folders:         folders,
		Restart:         model.Restart.ValueBool(),
		DeleteOnDestroy: model.DeleteOnDestroy.ValueBool(),
	}, diags
}

func resolveMacOSDockPathsForHome(diags *diag.Diagnostics, label string, paths []string, wantApp bool, homeDir string) []string {
	resolvedPaths := make([]string, 0, len(paths))
	for _, item := range paths {
		resolved, err := resolveMacOSDockPathForHome(label, item, wantApp, homeDir)
		if err != nil {
			diags.AddError("Invalid macOS Dock "+label, err.Error())
			continue
		}
		resolvedPaths = append(resolvedPaths, resolved)
	}
	return resolvedPaths
}

func resolveMacOSDockPathForHome(label string, item string, wantApp bool, homeDir string) (string, error) {
	path := strings.TrimSpace(item)
	if path == "" {
		return "", fmt.Errorf("%s entries must be non-empty paths", label)
	}
	if strings.Contains(path, "\x00") {
		return "", fmt.Errorf("%q must not contain NUL bytes", path)
	}

	resolved, err := expandHostPathForHome(path, homeDir)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", fmt.Errorf("path %q is not readable: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q must be a directory", resolved)
	}
	if wantApp && filepath.Ext(resolved) != ".app" {
		return "", fmt.Errorf("app path %q must end with .app", resolved)
	}
	return resolved, nil
}

func macOSDockModelFromSpec(ctx context.Context, model MacOSDockResourceModel, spec MacOSDockSpec) (MacOSDockResourceModel, error) {
	apps, appDiags := types.ListValueFrom(ctx, types.StringType, spec.Apps)
	if appDiags.HasError() {
		return model, diagnosticsError(appDiags)
	}
	folders, folderDiags := types.ListValueFrom(ctx, types.StringType, spec.Folders)
	if folderDiags.HasError() {
		return model, diagnosticsError(folderDiags)
	}

	model.ID = types.StringValue(macOSDockResourceID)
	model.Apps = apps
	model.Folders = folders
	if model.Restart.IsNull() || model.Restart.IsUnknown() {
		model.Restart = types.BoolValue(true)
	}
	if model.DeleteOnDestroy.IsNull() || model.DeleteOnDestroy.IsUnknown() {
		model.DeleteOnDestroy = types.BoolValue(false)
	}
	return model, nil
}

func macOSDockEntries(paths []string, tileType string) []string {
	entries := make([]string, 0, len(paths))
	for _, path := range paths {
		entries = append(entries, macOSDockEntry(path, tileType))
	}
	return entries
}

func macOSDockEntry(path string, tileType string) string {
	label := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	urlString := macOSDockFileURL(path)

	tileData := `"file-data"={"_CFURLString"=` + strconv.Quote(urlString) + `; "_CFURLStringType"=15;}; "file-label"=` + strconv.Quote(label) + `;`
	if tileType == "directory-tile" {
		tileData += ` arrangement=2; displayas=0; preferreditemsize="-1"; showas=1;`
	}
	return `{"tile-data"={` + tileData + `}; "tile-type"=` + strconv.Quote(tileType) + `;}`
}

func macOSDockFileURL(path string) string {
	cleaned := filepath.Clean(path)
	if !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return (&url.URL{Scheme: "file", Path: cleaned}).String()
}

func parseMacOSDockFileURLs(output string) []string {
	pattern := regexp.MustCompile(`"_CFURLString"\s*=\s*"([^"]+)"`)
	matches := pattern.FindAllStringSubmatch(output, -1)
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		parsed, err := url.Parse(match[1])
		if err != nil || parsed.Scheme != "file" {
			continue
		}
		path := strings.TrimSuffix(parsed.Path, "/")
		if path == "" {
			path = "/"
		}
		paths = append(paths, path)
	}
	return paths
}
