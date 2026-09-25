package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &MacOSFontResource{}
	_ resource.ResourceWithConfigure   = &MacOSFontResource{}
	_ resource.ResourceWithImportState = &MacOSFontResource{}
)

// macOS registers a font in a font directory by itself, but not while the
// quarantine attribute a downloader set is still on the file, and not before
// the font daemon rescans the directory. A package manager that drops files
// straight into ~/Library/Fonts therefore leaves fonts that no application can
// resolve: the file is on disk, `fc-list` finds it, and CoreText does not.
const (
	macOSFontQuarantineAttribute = "com.apple.quarantine"
	macOSFontDaemon              = "fontd"

	// Registration after a daemon restart is asynchronous, so a verification
	// that just missed it is retried rather than reported as a failure.
	macOSFontActivationAttempts = 5
	macOSFontActivationDelay    = 300 * time.Millisecond
)

type MacOSFontManager interface {
	// ActivePostScriptNames returns the subset of names macOS can resolve.
	ActivePostScriptNames(ctx context.Context, names []string) ([]string, error)
	ClearQuarantine(ctx context.Context, files []string) error
	RestartFontDaemon(ctx context.Context) error
}

type CLIMacOSFontManager struct {
	osascriptPath string
	xattrPath     string
	killallPath   string
	run           macOSCommandRunner
}

func NewCLIMacOSFontManager(osascriptPath string, xattrPath string, killallPath string) MacOSFontManager {
	return &CLIMacOSFontManager{
		osascriptPath: osascriptPath,
		xattrPath:     xattrPath,
		killallPath:   killallPath,
		run:           runMacOSCommand,
	}
}

// macOSFontProbe asks AppKit to resolve each PostScript name. A name resolves
// only when the font is registered for this user, which is exactly the state
// an application sees.
const macOSFontProbe = `function run(argv) {
  ObjC.import('AppKit');
  var active = [];
  for (var index = 0; index < argv.length; index++) {
    if (!$.NSFont.fontWithNameSize(argv[index], 12).isNil()) {
      active.push(argv[index]);
    }
  }
  return active.join('\n');
}`

func (m *CLIMacOSFontManager) ActivePostScriptNames(ctx context.Context, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if m.osascriptPath == "" {
		return nil, fmt.Errorf("osascript is required to read the registered fonts")
	}

	args := append([]string{"-l", "JavaScript", "-e", macOSFontProbe}, names...)
	output, err := m.run(ctx, m.osascriptPath, args...)
	if err != nil {
		return nil, err
	}

	active := make([]string, 0, len(names))
	for _, line := range strings.Split(string(output), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			active = append(active, trimmed)
		}
	}
	return active, nil
}

func (m *CLIMacOSFontManager) ClearQuarantine(ctx context.Context, files []string) error {
	if m.xattrPath == "" {
		return fmt.Errorf("xattr is required to clear the quarantine attribute")
	}

	for _, file := range files {
		output, err := m.run(ctx, m.xattrPath, file)
		if err != nil {
			return err
		}
		quarantined := false
		for _, line := range strings.Split(string(output), "\n") {
			if strings.TrimSpace(line) == macOSFontQuarantineAttribute {
				quarantined = true
				break
			}
		}
		if !quarantined {
			continue
		}
		if _, err := m.run(ctx, m.xattrPath, "-d", macOSFontQuarantineAttribute, file); err != nil {
			return err
		}
	}
	return nil
}

func (m *CLIMacOSFontManager) RestartFontDaemon(ctx context.Context) error {
	if m.killallPath == "" {
		return fmt.Errorf("killall is required to restart %s", macOSFontDaemon)
	}

	// The daemon is started on demand, so a machine where it is not running
	// has nothing to restart.
	if _, err := m.run(ctx, m.killallPath, macOSFontDaemon); err != nil {
		if strings.Contains(err.Error(), "No matching processes") {
			return nil
		}
		return err
	}
	return nil
}

type MacOSFontResource struct {
	manager MacOSFontManager
	homeDir string
}

type MacOSFontResourceModel struct {
	ID                types.String `tfsdk:"id"`
	Path              types.String `tfsdk:"path"`
	PathResolved      types.String `tfsdk:"path_resolved"`
	ClearQuarantine   types.Bool   `tfsdk:"clear_quarantine"`
	RestartFontDaemon types.Bool   `tfsdk:"restart_font_daemon"`
	Files             types.List   `tfsdk:"files"`
	PostScriptNames   types.List   `tfsdk:"postscript_names"`
	Families          types.List   `tfsdk:"families"`
	Active            types.Bool   `tfsdk:"active"`
}

func NewMacOSFontResource() resource.Resource {
	return &MacOSFontResource{}
}

func (r *MacOSFontResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mac_font"
}

func (r *MacOSFontResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Activates font files that are already installed in a macOS font directory, and reports the names applications resolve them by.\n\n" +
			"macOS auto-activates `~/Library/Fonts`, but not a file that still carries the quarantine attribute a downloader set, and not before the font daemon has rescanned the directory. " +
			"A font cask therefore installs files that CoreText never registers, so a terminal falls back to the last-resort glyph box for a Nerd Font symbol. This resource clears the quarantine attribute, restarts the font daemon when a font is still unregistered, and fails the apply when macOS still cannot resolve it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, the resolved `path`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Font file, or a directory holding font files, to activate. `~` expands to the target user's home directory. A directory is read without descending into subdirectories, because macOS activates a font directory and not its children. Supported suffixes: `" + strings.ReplaceAll(macOSFontExtensionList(), ", ", "`, `") + "`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"path_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Absolute `path` with `~` expanded.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"clear_quarantine": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Remove the `com.apple.quarantine` attribute from the font files. macOS refuses to auto-activate a quarantined font, and Homebrew sets the attribute on every downloaded cask artifact. Defaults to true.",
			},
			"restart_font_daemon": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Restart `fontd` when a font is still unregistered after the quarantine attribute is cleared, so it rescans the font directories. The daemon restarts on demand and running applications keep the fonts they already resolved. Defaults to true.",
			},
			"files": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Font files this resource activates.",
			},
			"postscript_names": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "PostScript names of every face in those files. This is the name macOS resolves a font by, and the name a preference such as iTerm's `Normal Font` stores.",
			},
			"families": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Family names of those faces, as a font picker lists them.",
			},
			"active": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "True when macOS resolves every PostScript name in `postscript_names`.",
			},
		},
	}
}

func (r *MacOSFontResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if !requireHostUserScope(data, "host_mac_font", &resp.Diagnostics) {
			return
		}
		if data.MacOSFontManager == nil {
			resp.Diagnostics.AddError("macOS fonts unavailable", "`host_mac_font` requires macOS.")
			return
		}
		r.manager = data.MacOSFontManager
		r.homeDir = data.HomeDir
	case MacOSFontManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected HostProviderData or MacOSFontManager, got %T.", req.ProviderData),
		)
	}
}

func (r *MacOSFontResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MacOSFontResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state := r.activate(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MacOSFontResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MacOSFontResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, err := r.inspect(ctx, state)
	if err != nil {
		// A font that disappeared is drift, not a failure to read. Dropping
		// the resource lets the next plan reinstall and reactivate it.
		resp.Diagnostics.AddWarning("Font is no longer readable", err.Error())
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *MacOSFontResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MacOSFontResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state := r.activate(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Delete leaves the font in place. The resource activates files that another
// resource installs, so removing it from the configuration stops managing the
// activation rather than uninstalling a font this provider never wrote.
func (r *MacOSFontResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
}

func (r *MacOSFontResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	trimmed := strings.TrimSpace(req.ID)
	if trimmed == "" {
		resp.Diagnostics.AddError(
			"Failed to import macOS font",
			"expected the import ID to be the font file or directory, such as \"~/Library/Fonts\"",
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("path"), trimmed)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("clear_quarantine"), true)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("restart_font_daemon"), true)...)
}

// activate clears what blocks auto-activation, nudges the font daemon when the
// fonts are still unregistered, and reports what macOS resolves afterwards.
func (r *MacOSFontResource) activate(ctx context.Context, model MacOSFontResourceModel, diags *diag.Diagnostics) MacOSFontResourceModel {
	if r.manager == nil {
		diags.AddError("macOS fonts unavailable", "`host_mac_font` requires macOS.")
		return model
	}

	faces, resolved, err := r.readFaces(model)
	if err != nil {
		diags.AddError("Failed to read macOS font", err.Error())
		return model
	}

	if model.ClearQuarantine.ValueBool() {
		if err := r.manager.ClearQuarantine(ctx, macOSFontFileList(faces)); err != nil {
			diags.AddError("Failed to clear the quarantine attribute", err.Error())
			return model
		}
	}

	inactive, err := r.inactiveNames(ctx, faces)
	if err != nil {
		diags.AddError("Failed to read the registered fonts", err.Error())
		return model
	}

	if len(inactive) > 0 && model.RestartFontDaemon.ValueBool() {
		if err := r.manager.RestartFontDaemon(ctx); err != nil {
			diags.AddError("Failed to restart the font daemon", err.Error())
			return model
		}
		inactive, err = r.waitForActivation(ctx, faces)
		if err != nil {
			diags.AddError("Failed to read the registered fonts", err.Error())
			return model
		}
	}

	next, listDiags := macOSFontModel(ctx, model, resolved, faces, len(inactive) == 0)
	diags.Append(listDiags...)
	if listDiags.HasError() {
		return model
	}

	if len(inactive) > 0 {
		diags.AddError(
			fmt.Sprintf("macOS did not register %s", pluralizeMacOSFonts(len(inactive))),
			fmt.Sprintf(
				"macOS still cannot resolve %s after activating %s.\n\nOpen the files in Font Book to see why it refuses them, or log out and back in to rebuild the font database.",
				strings.Join(inactive, ", "),
				resolved,
			),
		)
	}
	return next
}

// inspect reports the current state without changing anything.
func (r *MacOSFontResource) inspect(ctx context.Context, model MacOSFontResourceModel) (MacOSFontResourceModel, error) {
	if r.manager == nil {
		return model, fmt.Errorf("`host_mac_font` requires macOS")
	}

	faces, resolved, err := r.readFaces(model)
	if err != nil {
		return model, err
	}
	inactive, err := r.inactiveNames(ctx, faces)
	if err != nil {
		return model, err
	}

	next, diags := macOSFontModel(ctx, model, resolved, faces, len(inactive) == 0)
	if diags.HasError() {
		return model, diagnosticsError(diags)
	}
	return next, nil
}

func (r *MacOSFontResource) readFaces(model MacOSFontResourceModel) ([]macOSFontFace, string, error) {
	resolved, err := expandHostPathWithHome(model.Path.ValueString(), r.homeDir)
	if err != nil {
		return nil, "", err
	}

	files, err := macOSFontFilesAt(resolved)
	if err != nil {
		return nil, "", err
	}

	faces := make([]macOSFontFace, 0, len(files))
	for _, file := range files {
		parsed, err := readMacOSFontFaces(file)
		if err != nil {
			return nil, "", err
		}
		faces = append(faces, parsed...)
	}
	if len(faces) == 0 {
		return nil, "", fmt.Errorf("no font face in %s", resolved)
	}
	return faces, resolved, nil
}

func (r *MacOSFontResource) inactiveNames(ctx context.Context, faces []macOSFontFace) ([]string, error) {
	names := macOSFontPostScriptNames(faces)
	active, err := r.manager.ActivePostScriptNames(ctx, names)
	if err != nil {
		return nil, err
	}

	registered := make(map[string]struct{}, len(active))
	for _, name := range active {
		registered[name] = struct{}{}
	}

	inactive := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := registered[name]; !ok {
			inactive = append(inactive, name)
		}
	}
	return inactive, nil
}

func (r *MacOSFontResource) waitForActivation(ctx context.Context, faces []macOSFontFace) ([]string, error) {
	var inactive []string
	for attempt := 0; attempt < macOSFontActivationAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return inactive, ctx.Err()
		case <-time.After(macOSFontActivationDelay):
		}

		var err error
		inactive, err = r.inactiveNames(ctx, faces)
		if err != nil {
			return nil, err
		}
		if len(inactive) == 0 {
			return nil, nil
		}
	}
	return inactive, nil
}

func macOSFontModel(ctx context.Context, model MacOSFontResourceModel, resolved string, faces []macOSFontFace, active bool) (MacOSFontResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	files, fileDiags := types.ListValueFrom(ctx, types.StringType, macOSFontFileList(faces))
	diags.Append(fileDiags...)
	names, nameDiags := types.ListValueFrom(ctx, types.StringType, macOSFontPostScriptNames(faces))
	diags.Append(nameDiags...)
	families, familyDiags := types.ListValueFrom(ctx, types.StringType, macOSFontFamilies(faces))
	diags.Append(familyDiags...)
	if diags.HasError() {
		return model, diags
	}

	model.ID = types.StringValue(resolved)
	model.PathResolved = types.StringValue(resolved)
	model.Files = files
	model.PostScriptNames = names
	model.Families = families
	model.Active = types.BoolValue(active)
	return model, diags
}

func macOSFontFileList(faces []macOSFontFace) []string {
	return macOSFontUniqueValues(faces, func(face macOSFontFace) string { return face.File })
}

func macOSFontPostScriptNames(faces []macOSFontFace) []string {
	return macOSFontUniqueValues(faces, func(face macOSFontFace) string { return face.PostScriptName })
}

func macOSFontFamilies(faces []macOSFontFace) []string {
	return macOSFontUniqueValues(faces, func(face macOSFontFace) string { return face.Family })
}

// macOSFontUniqueValues keeps the attributes stable across runs: a file holds
// several faces of one family, and a directory listing must not reorder.
func macOSFontUniqueValues(faces []macOSFontFace, value func(macOSFontFace) string) []string {
	seen := make(map[string]struct{}, len(faces))
	values := make([]string, 0, len(faces))
	for _, face := range faces {
		item := value(face)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		values = append(values, item)
	}
	sort.Strings(values)
	return values
}

func pluralizeMacOSFonts(count int) string {
	if count == 1 {
		return "a font"
	}
	return fmt.Sprintf("%d fonts", count)
}
