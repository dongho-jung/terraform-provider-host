package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &MacOSDockAppResource{}
	_ resource.ResourceWithConfigure   = &MacOSDockAppResource{}
	_ resource.ResourceWithImportState = &MacOSDockAppResource{}
	_ resource.ResourceWithModifyPlan  = &MacOSDockAppResource{}

	_ resource.Resource                = &MacOSDockFolderResource{}
	_ resource.ResourceWithConfigure   = &MacOSDockFolderResource{}
	_ resource.ResourceWithImportState = &MacOSDockFolderResource{}
	_ resource.ResourceWithModifyPlan  = &MacOSDockFolderResource{}
)

const (
	macOSDockItemKindApp    = "app"
	macOSDockItemKindFolder = "folder"
)

type MacOSDockAppResource struct {
	item MacOSDockItemResource
}

type MacOSDockFolderResource struct {
	item MacOSDockItemResource
}

type MacOSDockItemResource struct {
	kind       string
	manager    MacOSDockManager
	homeDir    string
	runtimeDir string
}

type MacOSDockItemResourceModel struct {
	ID           types.String `tfsdk:"id"`
	Path         types.String `tfsdk:"path"`
	PathResolved types.String `tfsdk:"path_resolved"`
	Priority     types.Int64  `tfsdk:"priority"`
	Restart      types.Bool   `tfsdk:"restart"`
}

type macOSDockManagedState struct {
	Apps    map[string]macOSDockManagedItemState `json:"apps,omitempty"`
	Folders map[string]macOSDockManagedItemState `json:"folders,omitempty"`
}

type macOSDockManagedItemState struct {
	Path     string `json:"path"`
	Priority int64  `json:"priority"`
}

type macOSDockManagedItemSpec struct {
	ID       string
	Kind     string
	Path     string
	Priority int64
}

func NewMacOSDockAppResource() resource.Resource {
	return &MacOSDockAppResource{
		item: MacOSDockItemResource{kind: macOSDockItemKindApp},
	}
}

func NewMacOSDockFolderResource() resource.Resource {
	return &MacOSDockFolderResource{
		item: MacOSDockItemResource{kind: macOSDockItemKindFolder},
	}
}

func (r *MacOSDockAppResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mac_dock_app"
}

func (r *MacOSDockAppResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.item.configure(req, resp)
}

func (r *MacOSDockAppResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = r.item.schema("Manages one application in the macOS Dock persistent apps section.", "Application `.app` bundle path to show in the Dock.")
}

func (r *MacOSDockAppResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.item.modifyPlan(ctx, req, resp)
}

func (r *MacOSDockAppResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.item.importState(ctx, req, resp)
}

func (r *MacOSDockAppResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.item.create(ctx, req, resp)
}

func (r *MacOSDockAppResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	r.item.read(ctx, req, resp)
}

func (r *MacOSDockAppResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.item.update(ctx, req, resp)
}

func (r *MacOSDockAppResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	r.item.delete(ctx, req, resp)
}

func (r *MacOSDockFolderResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mac_dock_folder"
}

func (r *MacOSDockFolderResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.item.configure(req, resp)
}

func (r *MacOSDockFolderResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = r.item.schema("Manages one folder in the macOS Dock persistent folders section.", "Folder path to show in the Dock.")
}

func (r *MacOSDockFolderResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.item.modifyPlan(ctx, req, resp)
}

func (r *MacOSDockFolderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.item.importState(ctx, req, resp)
}

func (r *MacOSDockFolderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.item.create(ctx, req, resp)
}

func (r *MacOSDockFolderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	r.item.read(ctx, req, resp)
}

func (r *MacOSDockFolderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.item.update(ctx, req, resp)
}

func (r *MacOSDockFolderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	r.item.delete(ctx, req, resp)
}

func (r *MacOSDockItemResource) configure(req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(HostProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected HostProviderData, got %T.", req.ProviderData))
		return
	}
	if data.MacOSDockManager == nil {
		resp.Diagnostics.AddError("macOS Dock unavailable", "`host_mac_dock_app` and `host_mac_dock_folder` require the macOS `defaults` command.")
		return
	}
	r.manager = data.MacOSDockManager
	r.homeDir = data.HomeDir
	r.runtimeDir = data.RuntimeDir
}

func (r *MacOSDockItemResource) schema(description string, pathDescription string) schema.Schema {
	return schema.Schema{
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Stable identifier for this managed Dock item.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: pathDescription + " `~` is expanded to the provider `home_dir` and relative paths are resolved from the Terraform working directory.",
			},
			"path_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute Dock item path.",
			},
			"priority": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "Ordering priority within this Dock section. Lower numbers appear first. Priorities must be unique within apps or folders.",
			},
			"restart": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Restart Dock with `killall Dock` after writes or deletes.",
			},
		},
	}
}

func (r *MacOSDockItemResource) modifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan MacOSDockItemResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Path.IsNull() || plan.Path.IsUnknown() || plan.Priority.IsNull() || plan.Priority.IsUnknown() {
		return
	}

	resolved, err := resolveMacOSDockPathForHome(r.kind+"s", plan.Path.ValueString(), r.kind == macOSDockItemKindApp, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Invalid macOS Dock item", err.Error())
		return
	}
	plan.PathResolved = types.StringValue(resolved)
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *MacOSDockItemResource) importState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	state, err := r.importModel(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid macOS Dock import", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSDockItemResource) importModel(ctx context.Context, importID string) (MacOSDockItemResourceModel, error) {
	if r.manager == nil {
		return MacOSDockItemResourceModel{}, fmt.Errorf("macOS Dock manager unavailable")
	}

	priority, path, hasPriority, err := parseMacOSDockManagedItemImportID(importID)
	if err != nil {
		return MacOSDockItemResourceModel{}, err
	}
	pathResolved, err := resolveMacOSDockPathForHome(r.kind+"s", path, r.kind == macOSDockItemKindApp, r.homeDir)
	if err != nil {
		return MacOSDockItemResourceModel{}, err
	}

	dock, err := r.manager.ReadDock(ctx)
	if err != nil {
		return MacOSDockItemResourceModel{}, err
	}
	liveIndex := macOSDockPathIndex(macOSDockPathsForKind(dock, r.kind), pathResolved)
	if liveIndex < 0 {
		return MacOSDockItemResourceModel{}, fmt.Errorf("path %q is not present in the live macOS Dock", pathResolved)
	}
	if !hasPriority {
		priority = int64((liveIndex + 1) * 10)
	}

	id, err := newMacOSDockManagedItemID()
	if err != nil {
		return MacOSDockItemResourceModel{}, err
	}
	spec := macOSDockManagedItemSpec{
		ID:       id,
		Kind:     r.kind,
		Path:     pathResolved,
		Priority: priority,
	}
	id, err = importMacOSDockManagedItemForRuntime(spec, r.runtimeDir)
	if err != nil {
		return MacOSDockItemResourceModel{}, err
	}

	return MacOSDockItemResourceModel{
		ID:           types.StringValue(id),
		Path:         types.StringValue(path),
		PathResolved: types.StringValue(pathResolved),
		Priority:     types.Int64Value(priority),
		Restart:      types.BoolValue(true),
	}, nil
}

func (r *MacOSDockItemResource) create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MacOSDockItemResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := newMacOSDockManagedItemID()
	if err != nil {
		resp.Diagnostics.AddError("Failed to create macOS Dock item ID", err.Error())
		return
	}
	plan.ID = types.StringValue(id)

	state, err := r.sync(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync macOS Dock item", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSDockItemResource) read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MacOSDockItemResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateMacOSDockManagedItemID(state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid macOS Dock item ID", err.Error())
		return
	}

	item, exists, err := readMacOSDockManagedItemForRuntime(state.ID.ValueString(), r.kind, r.runtimeDir)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read macOS Dock item", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	state.PathResolved = types.StringValue(item.Path)
	state.Priority = types.Int64Value(item.Priority)
	if state.Restart.IsNull() || state.Restart.IsUnknown() {
		state.Restart = types.BoolValue(true)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSDockItemResource) update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MacOSDockItemResourceModel
	var state MacOSDockItemResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()
	if id == "" {
		var err error
		id, err = newMacOSDockManagedItemID()
		if err != nil {
			resp.Diagnostics.AddError("Failed to create macOS Dock item ID", err.Error())
			return
		}
	}
	plan.ID = types.StringValue(id)

	next, err := r.sync(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync macOS Dock item", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *MacOSDockItemResource) delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MacOSDockItemResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateMacOSDockManagedItemID(state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid macOS Dock item ID", err.Error())
		return
	}

	restart := state.Restart.IsNull() || state.Restart.IsUnknown() || state.Restart.ValueBool()
	if err := removeMacOSDockManagedItemForRuntime(ctx, r.manager, state.ID.ValueString(), r.kind, r.runtimeDir, restart); err != nil {
		resp.Diagnostics.AddError("Failed to delete macOS Dock item", err.Error())
	}
}

func (r *MacOSDockItemResource) sync(ctx context.Context, model MacOSDockItemResourceModel) (MacOSDockItemResourceModel, error) {
	if r.manager == nil {
		return model, fmt.Errorf("macOS Dock manager unavailable")
	}

	spec, diags := macOSDockManagedItemSpecFromModelForHome(model, r.kind, r.homeDir)
	if diags.HasError() {
		return model, diagnosticsError(diags)
	}
	restart := model.Restart.IsNull() || model.Restart.IsUnknown() || model.Restart.ValueBool()
	if err := upsertMacOSDockManagedItemForRuntime(ctx, r.manager, spec, r.runtimeDir, restart); err != nil {
		return model, err
	}

	model.ID = types.StringValue(spec.ID)
	model.PathResolved = types.StringValue(spec.Path)
	model.Priority = types.Int64Value(spec.Priority)
	if model.Restart.IsNull() || model.Restart.IsUnknown() {
		model.Restart = types.BoolValue(true)
	}
	return model, nil
}

func macOSDockManagedItemSpecFromModelForHome(model MacOSDockItemResourceModel, kind string, homeDir string) (macOSDockManagedItemSpec, diag.Diagnostics) {
	var diags diag.Diagnostics
	id := model.ID.ValueString()
	if err := validateMacOSDockManagedItemID(id); err != nil {
		diags.AddError("Invalid macOS Dock item ID", err.Error())
		return macOSDockManagedItemSpec{}, diags
	}
	if model.Path.IsNull() || model.Path.IsUnknown() {
		diags.AddError("Invalid macOS Dock item", "`path` must be known.")
		return macOSDockManagedItemSpec{}, diags
	}
	if model.Priority.IsNull() || model.Priority.IsUnknown() {
		diags.AddError("Invalid macOS Dock item", "`priority` must be known.")
		return macOSDockManagedItemSpec{}, diags
	}
	path, err := resolveMacOSDockPathForHome(kind+"s", model.Path.ValueString(), kind == macOSDockItemKindApp, homeDir)
	if err != nil {
		diags.AddError("Invalid macOS Dock item", err.Error())
		return macOSDockManagedItemSpec{}, diags
	}

	return macOSDockManagedItemSpec{
		ID:       id,
		Kind:     kind,
		Path:     path,
		Priority: model.Priority.ValueInt64(),
	}, diags
}

func newMacOSDockManagedItemID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return "hdi-" + hex.EncodeToString(bytes[:]), nil
}

func validateMacOSDockManagedItemID(id string) error {
	if len(id) != 36 || id[:4] != "hdi-" {
		return fmt.Errorf("dock item ID must be a generated hdi-* identifier")
	}
	_, err := hex.DecodeString(id[4:])
	if err != nil {
		return fmt.Errorf("dock item ID must contain hex characters")
	}
	return nil
}

func parseMacOSDockManagedItemImportID(importID string) (int64, string, bool, error) {
	importID = strings.TrimSpace(importID)
	if importID == "" {
		return 0, "", false, fmt.Errorf("import ID must be a path or priority,path")
	}

	priorityText, path, hasSeparator := strings.Cut(importID, ",")
	if hasSeparator {
		if priority, err := strconv.ParseInt(strings.TrimSpace(priorityText), 10, 64); err == nil {
			path = strings.TrimSpace(path)
			if path == "" {
				return 0, "", false, fmt.Errorf("import path must be non-empty")
			}
			return priority, path, true, nil
		}
	}

	return 0, importID, false, nil
}

func macOSDockPathsForKind(spec MacOSDockSpec, kind string) []string {
	if kind == macOSDockItemKindFolder {
		return spec.Folders
	}
	return spec.Apps
}

func macOSDockPathIndex(paths []string, path string) int {
	for index, candidate := range paths {
		if candidate == path {
			return index
		}
	}
	return -1
}

func importMacOSDockManagedItemForRuntime(spec macOSDockManagedItemSpec, runtimeDir string) (string, error) {
	importedID := spec.ID
	err := withLockedMacOSDockManagedState(runtimeDir, func() error {
		state, err := readMacOSDockManagedStateForRuntime(runtimeDir)
		if err != nil {
			return err
		}

		for id, item := range macOSDockManagedStateItemsForKind(&state, spec.Kind) {
			if item.Path == spec.Path {
				spec.ID = id
				importedID = id
				break
			}
		}
		if err := upsertMacOSDockManagedStateItem(&state, spec); err != nil {
			return err
		}
		return writeMacOSDockManagedStateForRuntime(state, runtimeDir)
	})
	return importedID, err
}

func upsertMacOSDockManagedItemForRuntime(ctx context.Context, manager MacOSDockManager, spec macOSDockManagedItemSpec, runtimeDir string, restart bool) error {
	return withLockedMacOSDockManagedState(runtimeDir, func() error {
		state, err := readMacOSDockManagedStateForRuntime(runtimeDir)
		if err != nil {
			return err
		}
		if err := upsertMacOSDockManagedStateItem(&state, spec); err != nil {
			return err
		}
		return writeMacOSDockManagedStateAndDockForRuntime(ctx, manager, state, runtimeDir, restart)
	})
}

func removeMacOSDockManagedItemForRuntime(ctx context.Context, manager MacOSDockManager, id string, kind string, runtimeDir string, restart bool) error {
	return withLockedMacOSDockManagedState(runtimeDir, func() error {
		state, exists, err := readMacOSDockManagedStateIfExistsForRuntime(runtimeDir)
		if err != nil || !exists {
			return err
		}
		delete(macOSDockManagedStateItemsForKind(&state, kind), id)
		return writeMacOSDockManagedStateAndDockForRuntime(ctx, manager, state, runtimeDir, restart)
	})
}

func readMacOSDockManagedItemForRuntime(id string, kind string, runtimeDir string) (macOSDockManagedItemState, bool, error) {
	state, exists, err := readMacOSDockManagedStateIfExistsForRuntime(runtimeDir)
	if err != nil || !exists {
		return macOSDockManagedItemState{}, false, err
	}
	item, ok := macOSDockManagedStateItemsForKind(&state, kind)[id]
	return item, ok, nil
}

func upsertMacOSDockManagedStateItem(state *macOSDockManagedState, spec macOSDockManagedItemSpec) error {
	items := macOSDockManagedStateItemsForKind(state, spec.Kind)
	for id, item := range items {
		if id == spec.ID {
			continue
		}
		if item.Priority == spec.Priority {
			return fmt.Errorf("macOS Dock %s priority %d is already used by %q", spec.Kind, spec.Priority, item.Path)
		}
		if item.Path == spec.Path {
			return fmt.Errorf("macOS Dock %s path %q is already managed by another resource", spec.Kind, spec.Path)
		}
	}
	items[spec.ID] = macOSDockManagedItemState{
		Path:     spec.Path,
		Priority: spec.Priority,
	}
	return nil
}

func writeMacOSDockManagedStateAndDockForRuntime(ctx context.Context, manager MacOSDockManager, state macOSDockManagedState, runtimeDir string, restart bool) error {
	spec, err := macOSDockSpecFromManagedState(state)
	if err != nil {
		return err
	}
	if err := manager.WriteDock(ctx, spec); err != nil {
		return err
	}
	if restart {
		if err := manager.RestartDock(ctx); err != nil {
			return err
		}
	}
	if err := writeMacOSDockManagedStateForRuntime(state, runtimeDir); err != nil {
		return err
	}
	return nil
}

func macOSDockSpecFromManagedState(state macOSDockManagedState) (MacOSDockSpec, error) {
	apps, err := sortedMacOSDockManagedPaths(state.Apps, macOSDockItemKindApp)
	if err != nil {
		return MacOSDockSpec{}, err
	}
	folders, err := sortedMacOSDockManagedPaths(state.Folders, macOSDockItemKindFolder)
	if err != nil {
		return MacOSDockSpec{}, err
	}
	return MacOSDockSpec{Apps: apps, Folders: folders}, nil
}

func sortedMacOSDockManagedPaths(items map[string]macOSDockManagedItemState, kind string) ([]string, error) {
	ordered := make([]struct {
		id   string
		item macOSDockManagedItemState
	}, 0, len(items))
	seenPriorities := map[int64]string{}
	seenPaths := map[string]string{}
	for id, item := range items {
		if priorID, ok := seenPriorities[item.Priority]; ok {
			return nil, fmt.Errorf("macOS Dock %s priority %d is used by both %q and %q", kind, item.Priority, priorID, id)
		}
		if priorID, ok := seenPaths[item.Path]; ok {
			return nil, fmt.Errorf("macOS Dock %s path %q is used by both %q and %q", kind, item.Path, priorID, id)
		}
		seenPriorities[item.Priority] = id
		seenPaths[item.Path] = id
		ordered = append(ordered, struct {
			id   string
			item macOSDockManagedItemState
		}{id: id, item: item})
	}
	sort.Slice(ordered, func(i int, j int) bool {
		if ordered[i].item.Priority != ordered[j].item.Priority {
			return ordered[i].item.Priority < ordered[j].item.Priority
		}
		return ordered[i].id < ordered[j].id
	})

	paths := make([]string, 0, len(ordered))
	for _, entry := range ordered {
		paths = append(paths, entry.item.Path)
	}
	return paths, nil
}

func macOSDockManagedStateItemsForKind(state *macOSDockManagedState, kind string) map[string]macOSDockManagedItemState {
	switch kind {
	case macOSDockItemKindFolder:
		if state.Folders == nil {
			state.Folders = map[string]macOSDockManagedItemState{}
		}
		return state.Folders
	default:
		if state.Apps == nil {
			state.Apps = map[string]macOSDockManagedItemState{}
		}
		return state.Apps
	}
}

func readMacOSDockManagedStateForRuntime(runtimeDir string) (macOSDockManagedState, error) {
	state, _, err := readMacOSDockManagedStateIfExistsForRuntime(runtimeDir)
	return state, err
}

func readMacOSDockManagedStateIfExistsForRuntime(runtimeDir string) (macOSDockManagedState, bool, error) {
	statePath, err := macOSDockManagedStatePathForRuntime(runtimeDir)
	if err != nil {
		return macOSDockManagedState{}, false, err
	}
	content, err := os.ReadFile(statePath)
	if os.IsNotExist(err) {
		return emptyMacOSDockManagedState(), false, nil
	}
	if err != nil {
		return macOSDockManagedState{}, false, fmt.Errorf("read macOS Dock state %q: %w", statePath, err)
	}

	var state macOSDockManagedState
	if err := json.Unmarshal(content, &state); err != nil {
		return macOSDockManagedState{}, false, fmt.Errorf("parse macOS Dock state %q: %w", statePath, err)
	}
	ensureMacOSDockManagedStateMaps(&state)
	return state, true, nil
}

func writeMacOSDockManagedStateForRuntime(state macOSDockManagedState, runtimeDir string) error {
	statePath, err := macOSDockManagedStatePathForRuntime(runtimeDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return fmt.Errorf("create macOS Dock state directory for %q: %w", statePath, err)
	}
	ensureMacOSDockManagedStateMaps(&state)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode macOS Dock state %q: %w", statePath, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		return fmt.Errorf("write macOS Dock state %q: %w", statePath, err)
	}
	return nil
}

func macOSDockManagedStatePathForRuntime(runtimeDir string) (string, error) {
	stateDir, err := providerRuntimeSubdirForRuntime(runtimeDir, "mac_dock")
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "state.json"), nil
}

func withLockedMacOSDockManagedState(runtimeDir string, fn func() error) error {
	statePath, err := macOSDockManagedStatePathForRuntime(runtimeDir)
	if err != nil {
		return err
	}
	lock, err := lockHostFile(statePath)
	if err != nil {
		return err
	}
	defer lock.close()
	return fn()
}

func emptyMacOSDockManagedState() macOSDockManagedState {
	return macOSDockManagedState{
		Apps:    map[string]macOSDockManagedItemState{},
		Folders: map[string]macOSDockManagedItemState{},
	}
}

func ensureMacOSDockManagedStateMaps(state *macOSDockManagedState) {
	if state.Apps == nil {
		state.Apps = map[string]macOSDockManagedItemState{}
	}
	if state.Folders == nil {
		state.Folders = map[string]macOSDockManagedItemState{}
	}
}
