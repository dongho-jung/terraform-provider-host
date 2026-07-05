package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tfpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &MacOSLoginItemResource{}
	_ resource.ResourceWithConfigure   = &MacOSLoginItemResource{}
	_ resource.ResourceWithImportState = &MacOSLoginItemResource{}
	_ resource.ResourceWithModifyPlan  = &MacOSLoginItemResource{}
)

type MacOSLoginItemResource struct {
	manager MacOSLoginItemManager
	homeDir string
}

type MacOSLoginItemResourceModel struct {
	ID           types.String `tfsdk:"id"`
	Path         types.String `tfsdk:"path"`
	PathResolved types.String `tfsdk:"path_resolved"`
	Name         types.String `tfsdk:"name"`
	Hidden       types.Bool   `tfsdk:"hidden"`
}

type MacOSLoginItemSpec struct {
	Path         string
	PathResolved string
	Hidden       bool
}

type MacOSLoginItemStatus struct {
	Path         string
	PathResolved string
	Name         string
	Hidden       bool
}

type MacOSLoginItemManager interface {
	LoginItemStatus(ctx context.Context, path string) (MacOSLoginItemStatus, bool, error)
	EnsureLoginItem(ctx context.Context, spec MacOSLoginItemSpec) (MacOSLoginItemStatus, error)
	DeleteLoginItem(ctx context.Context, path string) error
}

type CLIMacOSLoginItemManager struct {
	osascriptPath string
	homeDir       string
}

func NewCLIMacOSLoginItemManager(osascriptPath string, homeDirs ...string) MacOSLoginItemManager {
	manager := &CLIMacOSLoginItemManager{
		osascriptPath: osascriptPath,
	}
	if len(homeDirs) > 0 {
		manager.homeDir = homeDirs[0]
	}
	return manager
}

func NewMacOSLoginItemResource() resource.Resource {
	return &MacOSLoginItemResource{}
}

func (r *MacOSLoginItemResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mac_login_item"
}

func (r *MacOSLoginItemResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one macOS Login Item by application bundle path.",
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
				MarkdownDescription: "Application bundle path to open at login. `~` is expanded to the provider `home_dir` and relative paths are resolved from the Terraform working directory.",
			},
			"path_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute application bundle path.",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Login item name reported by macOS.",
			},
			"hidden": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Whether macOS should hide the app when opening it at login. Defaults to false.",
			},
		},
	}
}

func (r *MacOSLoginItemResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.MacOSLoginItemManager == nil {
			resp.Diagnostics.AddError("macOS Login Items unavailable", "`host_mac_login_item` requires the macOS `osascript` command.")
			return
		}
		r.manager = data.MacOSLoginItemManager
		r.homeDir = data.HomeDir
	case MacOSLoginItemManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or MacOSLoginItemManager, got %T.", req.ProviderData),
		)
	}
}

func (r *MacOSLoginItemResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan MacOSLoginItemResourceModel
	var state MacOSLoginItemResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	}
	if resp.Diagnostics.HasError() || plan.Path.IsNull() || plan.Path.IsUnknown() || plan.Hidden.IsNull() || plan.Hidden.IsUnknown() {
		return
	}

	spec, err := macOSLoginItemSpecFromModelForHome(plan, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Invalid macOS Login Item", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.Path.ValueString())
	plan.PathResolved = types.StringValue(spec.PathResolved)
	requireReplaceIfResolvedPathChanged(req, resp, tfpath.Root("path"), state.Path, state.PathResolved, spec.PathResolved, func(value string) (string, error) {
		return resolveMacOSLoginItemPathForHome(value, r.homeDir)
	})
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *MacOSLoginItemResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MacOSLoginItemResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncLoginItem(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync macOS Login Item", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSLoginItemResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MacOSLoginItemResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.manager == nil {
		resp.Diagnostics.AddError("macOS Login Items unavailable", "`host_mac_login_item` requires the macOS `osascript` command.")
		return
	}

	spec, err := macOSLoginItemSpecFromModelForHome(state, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Invalid macOS Login Item state", err.Error())
		return
	}
	status, exists, err := r.manager.LoginItemStatus(ctx, spec.PathResolved)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read macOS Login Item", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	next := macOSLoginItemModelFromStatus(state, status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *MacOSLoginItemResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MacOSLoginItemResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncLoginItem(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync macOS Login Item", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSLoginItemResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MacOSLoginItemResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.manager == nil {
		resp.Diagnostics.AddError("macOS Login Items unavailable", "`host_mac_login_item` requires the macOS `osascript` command.")
		return
	}

	spec, err := macOSLoginItemSpecFromModelForHome(state, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Invalid macOS Login Item state", err.Error())
		return
	}
	if err := r.manager.DeleteLoginItem(ctx, spec.PathResolved); err != nil {
		resp.Diagnostics.AddError("Failed to delete macOS Login Item", err.Error())
	}
}

func (r *MacOSLoginItemResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, tfpath.Root("path"), req, resp)
}

func (r *MacOSLoginItemResource) syncLoginItem(ctx context.Context, model MacOSLoginItemResourceModel) (MacOSLoginItemResourceModel, error) {
	if r.manager == nil {
		return model, fmt.Errorf("macOS Login Items manager unavailable")
	}

	spec, err := macOSLoginItemSpecFromModelForHome(model, r.homeDir)
	if err != nil {
		return model, err
	}
	status, err := r.manager.EnsureLoginItem(ctx, spec)
	if err != nil {
		return model, err
	}

	return macOSLoginItemModelFromStatus(model, status), nil
}

func (m *CLIMacOSLoginItemManager) LoginItemStatus(ctx context.Context, path string) (MacOSLoginItemStatus, bool, error) {
	if m.osascriptPath == "" {
		return MacOSLoginItemStatus{}, false, fmt.Errorf("osascript command not found")
	}
	pathResolved, err := resolveMacOSLoginItemPathForHome(path, m.homeDir)
	if err != nil {
		return MacOSLoginItemStatus{}, false, err
	}

	items, err := m.readLoginItems(ctx)
	if err != nil {
		return MacOSLoginItemStatus{}, false, err
	}
	for _, item := range items {
		if sameMacOSLoginItemPath(item.PathResolved, pathResolved) {
			return item, true, nil
		}
	}

	return MacOSLoginItemStatus{}, false, nil
}

func (m *CLIMacOSLoginItemManager) EnsureLoginItem(ctx context.Context, spec MacOSLoginItemSpec) (MacOSLoginItemStatus, error) {
	if m.osascriptPath == "" {
		return MacOSLoginItemStatus{}, fmt.Errorf("osascript command not found")
	}
	pathResolved, err := resolveMacOSLoginItemPathForHome(spec.PathResolved, m.homeDir)
	if err != nil {
		return MacOSLoginItemStatus{}, err
	}
	if err := ensureMacOSLoginItemPath(pathResolved); err != nil {
		return MacOSLoginItemStatus{}, err
	}

	hiddenValue := "false"
	if spec.Hidden {
		hiddenValue = "true"
	}
	_, err = m.runOSAScript(ctx, []string{
		"on run argv",
		"set targetPath to item 1 of argv",
		"set targetHidden to item 2 of argv is \"true\"",
		"tell application \"System Events\"",
		"set existingItem to missing value",
		"repeat with itemRef in every login item",
		"if path of itemRef is targetPath then set existingItem to itemRef",
		"end repeat",
		"if existingItem is missing value then",
		"make login item at end with properties {path:targetPath, hidden:targetHidden}",
		"else",
		"set hidden of existingItem to targetHidden",
		"end if",
		"end tell",
		"end run",
	}, pathResolved, hiddenValue)
	if err != nil {
		return MacOSLoginItemStatus{}, err
	}

	status, exists, err := m.LoginItemStatus(ctx, pathResolved)
	if err != nil {
		return MacOSLoginItemStatus{}, err
	}
	if !exists {
		return MacOSLoginItemStatus{}, fmt.Errorf("login item for %q did not appear after creation", pathResolved)
	}
	return status, nil
}

func (m *CLIMacOSLoginItemManager) DeleteLoginItem(ctx context.Context, path string) error {
	if m.osascriptPath == "" {
		return fmt.Errorf("osascript command not found")
	}
	pathResolved, err := resolveMacOSLoginItemPathForHome(path, m.homeDir)
	if err != nil {
		return err
	}

	_, err = m.runOSAScript(ctx, []string{
		"on run argv",
		"set targetPath to item 1 of argv",
		"tell application \"System Events\"",
		"repeat with itemRef in every login item",
		"if path of itemRef is targetPath then delete itemRef",
		"end repeat",
		"end tell",
		"end run",
	}, pathResolved)
	return err
}

func (m *CLIMacOSLoginItemManager) readLoginItems(ctx context.Context) ([]MacOSLoginItemStatus, error) {
	out, err := m.runOSAScript(ctx, []string{
		"set output to \"\"",
		"tell application \"System Events\"",
		"repeat with itemRef in every login item",
		"set itemName to name of itemRef",
		"set itemPath to path of itemRef",
		"set itemHidden to hidden of itemRef",
		"set output to output & itemName & tab & itemPath & tab & itemHidden & linefeed",
		"end repeat",
		"end tell",
		"return output",
	})
	if err != nil {
		return nil, err
	}
	return parseMacOSLoginItemsForHome(string(out), m.homeDir)
}

func (m *CLIMacOSLoginItemManager) runOSAScript(ctx context.Context, scriptLines []string, args ...string) ([]byte, error) {
	commandArgs := make([]string, 0, len(scriptLines)*2+len(args))
	for _, line := range scriptLines {
		commandArgs = append(commandArgs, "-e", line)
	}
	commandArgs = append(commandArgs, args...)

	cmd := exec.CommandContext(ctx, m.osascriptPath, commandArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("run osascript: %w%s", err, commandOutputSuffix(out))
	}
	return out, nil
}

func macOSLoginItemSpecFromModel(model MacOSLoginItemResourceModel) (MacOSLoginItemSpec, error) {
	return macOSLoginItemSpecFromModelForHome(model, "")
}

func macOSLoginItemSpecFromModelForHome(model MacOSLoginItemResourceModel, homeDir string) (MacOSLoginItemSpec, error) {
	if model.Path.IsNull() || model.Path.IsUnknown() {
		return MacOSLoginItemSpec{}, fmt.Errorf("path must be known")
	}
	pathResolved, err := resolveMacOSLoginItemPathForHome(model.Path.ValueString(), homeDir)
	if err != nil {
		return MacOSLoginItemSpec{}, err
	}
	if filepath.Ext(pathResolved) != ".app" {
		return MacOSLoginItemSpec{}, fmt.Errorf("path %q must point to an .app bundle", pathResolved)
	}

	hidden := false
	if !model.Hidden.IsNull() && !model.Hidden.IsUnknown() {
		hidden = model.Hidden.ValueBool()
	}
	return MacOSLoginItemSpec{
		Path:         model.Path.ValueString(),
		PathResolved: pathResolved,
		Hidden:       hidden,
	}, nil
}

func macOSLoginItemModelFromStatus(model MacOSLoginItemResourceModel, status MacOSLoginItemStatus) MacOSLoginItemResourceModel {
	model.ID = types.StringValue(model.Path.ValueString())
	model.PathResolved = types.StringValue(status.PathResolved)
	model.Name = types.StringValue(status.Name)
	model.Hidden = types.BoolValue(status.Hidden)
	return model
}

func parseMacOSLoginItems(output string) ([]MacOSLoginItemStatus, error) {
	return parseMacOSLoginItemsForHome(output, "")
}

func parseMacOSLoginItemsForHome(output string, homeDir string) ([]MacOSLoginItemStatus, error) {
	lines := strings.Split(output, "\n")
	items := make([]MacOSLoginItemStatus, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return nil, fmt.Errorf("cannot parse login item line %q", line)
		}
		pathResolved, err := resolveMacOSLoginItemPathForHome(parts[1], homeDir)
		if err != nil {
			return nil, err
		}
		hidden, err := parseAppleScriptBool(parts[2])
		if err != nil {
			return nil, fmt.Errorf("cannot parse hidden flag for login item %q: %w", parts[0], err)
		}
		items = append(items, MacOSLoginItemStatus{
			Path:         parts[1],
			PathResolved: pathResolved,
			Name:         parts[0],
			Hidden:       hidden,
		})
	}
	return items, nil
}

func resolveMacOSLoginItemPathForHome(path string, homeDir string) (string, error) {
	if strings.Contains(path, "\x00") {
		return "", fmt.Errorf("path must not contain NUL bytes")
	}
	return expandHostPathWithHome(path, homeDir)
}

func ensureMacOSLoginItemPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("application bundle %q does not exist", path)
		}
		return fmt.Errorf("read application bundle %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("application bundle %q must be a directory", path)
	}
	if filepath.Ext(path) != ".app" {
		return fmt.Errorf("application bundle %q must end with .app", path)
	}
	return nil
}

func parseAppleScriptBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("expected true or false, got %q", value)
	}
}

func sameMacOSLoginItemPath(left string, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
