package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &HostSSHConfigHostResource{}
	_ resource.ResourceWithConfigure   = &HostSSHConfigHostResource{}
	_ resource.ResourceWithImportState = &HostSSHConfigHostResource{}
	_ resource.ResourceWithModifyPlan  = &HostSSHConfigHostResource{}
)

const (
	defaultSSHConfigPath               = "~/.ssh/config"
	hostSSHConfigBlockBeginPrefix      = "# BEGIN Terraform host_ssh_config_host "
	hostSSHConfigBlockEndPrefix        = "# END Terraform host_ssh_config_host "
	hostSSHConfigDefaultFilePermission = 0o600
	hostSSHConfigDefaultDirPermission  = 0o700
)

type HostSSHConfigHostResource struct {
	homeDir string
}

type HostSSHConfigHostResourceModel struct {
	ID                   types.String `tfsdk:"id"`
	ConfigPath           types.String `tfsdk:"config_path"`
	ConfigPathResolved   types.String `tfsdk:"config_path_resolved"`
	Host                 types.String `tfsdk:"host"`
	HostName             types.String `tfsdk:"hostname"`
	User                 types.String `tfsdk:"user"`
	Port                 types.Int64  `tfsdk:"port"`
	IdentityFile         types.String `tfsdk:"identity_file"`
	IdentityFileResolved types.String `tfsdk:"identity_file_resolved"`
	IdentitiesOnly       types.Bool   `tfsdk:"identities_only"`
	ForwardAgent         types.Bool   `tfsdk:"forward_agent"`
	ProxyJump            types.String `tfsdk:"proxy_jump"`
	ProxyCommand         types.String `tfsdk:"proxy_command"`
	ExtraOptions         types.Map    `tfsdk:"extra_options"`
	AdoptExisting        types.Bool   `tfsdk:"adopt_existing"`
	RenderedBlock        types.String `tfsdk:"rendered_block"`
}

type hostSSHConfigHostSpec struct {
	ID                   string
	ConfigPath           string
	ConfigPathResolved   string
	Host                 string
	HostName             string
	User                 string
	Port                 int64
	IdentityFile         string
	IdentityFileResolved string
	IdentitiesOnly       *bool
	ForwardAgent         *bool
	ProxyJump            string
	ProxyCommand         string
	ExtraOptions         map[string]string
	AdoptExisting        bool
}

func NewHostSSHConfigHostResource() resource.Resource {
	return &HostSSHConfigHostResource{}
}

func (r *HostSSHConfigHostResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_config_host"
}

func (r *HostSSHConfigHostResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(HostProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected HostProviderData, got %T.", req.ProviderData))
		return
	}
	r.homeDir = data.HomeDir
}

func (r *HostSSHConfigHostResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one Terraform-owned `Host` block in an OpenSSH client config file.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Provider-generated identifier for the managed SSH config block.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"config_path": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(defaultSSHConfigPath),
				MarkdownDescription: "OpenSSH client config path. Defaults to `~/.ssh/config`.",
			},
			"config_path_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute SSH config path.",
			},
			"host": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Single OpenSSH `Host` pattern or alias managed by this resource, such as `github.com` or `work-bastion`.",
			},
			"hostname": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Value for the `HostName` directive.",
			},
			"user": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Value for the `User` directive.",
			},
			"port": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Value for the `Port` directive.",
			},
			"identity_file": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Value for the `IdentityFile` directive. `~` is preserved in the rendered config and expanded for `identity_file_resolved`.",
			},
			"identity_file_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute identity file path, when `identity_file` is set.",
			},
			"identities_only": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When set, renders `IdentitiesOnly yes` or `IdentitiesOnly no`.",
			},
			"forward_agent": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When set, renders `ForwardAgent yes` or `ForwardAgent no`.",
			},
			"proxy_jump": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Value for the `ProxyJump` directive.",
			},
			"proxy_command": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Value for the `ProxyCommand` directive. This is rendered as a raw directive value.",
			},
			"extra_options": schema.MapAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				MarkdownDescription: "Additional SSH config directives rendered after first-class attributes. Keys must be directive names and values are rendered as raw directive values.",
			},
			"adopt_existing": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When true, replace an existing unmanaged `Host` block with the same alias with the Terraform-managed block during create. Defaults to false.",
			},
			"rendered_block": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Last rendered Terraform-owned SSH config block. Used to detect drift.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *HostSSHConfigHostResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostSSHConfigHostResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !hostSSHConfigPlanReady(plan) {
		return
	}

	spec, diags := hostSSHConfigHostSpecFromModelForHome(ctx, plan, r.homeDir)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = types.StringValue(spec.ID)
	plan.ConfigPathResolved = types.StringValue(spec.ConfigPathResolved)
	plan.IdentityFileResolved = optionalStringStateValue(spec.IdentityFileResolved)
	plan.RenderedBlock = types.StringValue(renderHostSSHConfigBlock(spec))
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostSSHConfigHostResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostSSHConfigHostResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := syncHostSSHConfigHostForHome(ctx, plan, nil, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync SSH config host", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSSHConfigHostResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostSSHConfigHostResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	spec, diags := hostSSHConfigHostSpecFromModelForHome(ctx, state, r.homeDir)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var rendered string
	var exists bool
	if err := withLockedSSHConfigForHome(ctx, r.homeDir, spec.ConfigPathResolved, func(path string) error {
		var err error
		rendered, exists, err = readHostSSHConfigManagedBlock(path, spec.ID)
		if err != nil || exists || !spec.AdoptExisting {
			return err
		}
		rendered, exists, err = readUnmanagedSSHConfigHostBlock(path, spec.Host)
		return err
	}); err != nil {
		resp.Diagnostics.AddError("Failed to read SSH config host", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(spec.ID)
	state.ConfigPathResolved = types.StringValue(spec.ConfigPathResolved)
	state.IdentityFileResolved = optionalStringStateValue(spec.IdentityFileResolved)
	state.RenderedBlock = types.StringValue(rendered)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSSHConfigHostResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostSSHConfigHostResourceModel
	var state HostSSHConfigHostResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, err := syncHostSSHConfigHostForHome(ctx, plan, &state, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync SSH config host", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostSSHConfigHostResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostSSHConfigHostResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	spec, diags := hostSSHConfigHostSpecFromModelForHome(ctx, state, r.homeDir)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := withLockedSSHConfigForHome(ctx, r.homeDir, spec.ConfigPathResolved, func(path string) error {
		return removeHostSSHConfigHostBlock(path, spec.ID, spec.Host, spec.AdoptExisting)
	}); err != nil {
		resp.Diagnostics.AddError("Failed to delete SSH config host", err.Error())
	}
}

func (r *HostSSHConfigHostResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	configPath, host, err := parseHostSSHConfigHostImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid SSH config host import ID", err.Error())
		return
	}
	configPathResolved, err := resolveSSHConfigPathForHome(configPath, r.homeDir)
	if err != nil {
		resp.Diagnostics.AddError("Invalid SSH config path", err.Error())
		return
	}

	state := HostSSHConfigHostResourceModel{
		ID:                 types.StringValue(hostSSHConfigHostID(configPathResolved, host)),
		ConfigPath:         types.StringValue(configPath),
		ConfigPathResolved: types.StringValue(configPathResolved),
		Host:               types.StringValue(host),
		ExtraOptions:       types.MapNull(types.StringType),
		AdoptExisting:      types.BoolValue(true),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func syncHostSSHConfigHostForHome(ctx context.Context, plan HostSSHConfigHostResourceModel, prior *HostSSHConfigHostResourceModel, homeDir string) (HostSSHConfigHostResourceModel, error) {
	spec, diags := hostSSHConfigHostSpecFromModelForHome(ctx, plan, homeDir)
	if diags.HasError() {
		return plan, diagnosticsError(diags)
	}

	if prior != nil {
		priorSpec, priorDiags := hostSSHConfigHostSpecFromModelForHome(ctx, *prior, homeDir)
		if priorDiags.HasError() {
			return plan, diagnosticsError(priorDiags)
		}
		if priorSpec.ID != spec.ID || priorSpec.ConfigPathResolved != spec.ConfigPathResolved {
			if err := withLockedSSHConfigForHome(ctx, homeDir, priorSpec.ConfigPathResolved, func(path string) error {
				return removeHostSSHConfigHostBlock(path, priorSpec.ID, priorSpec.Host, priorSpec.AdoptExisting)
			}); err != nil {
				return plan, err
			}
		}
	}

	rendered := renderHostSSHConfigBlock(spec)
	if err := withLockedSSHConfigForHome(ctx, homeDir, spec.ConfigPathResolved, func(path string) error {
		return upsertHostSSHConfigManagedBlock(path, spec.ID, spec.Host, rendered, spec.AdoptExisting)
	}); err != nil {
		return plan, err
	}

	plan.ID = types.StringValue(spec.ID)
	plan.ConfigPathResolved = types.StringValue(spec.ConfigPathResolved)
	plan.IdentityFileResolved = optionalStringStateValue(spec.IdentityFileResolved)
	plan.RenderedBlock = types.StringValue(rendered)
	return plan, nil
}

func hostSSHConfigHostSpecFromModel(ctx context.Context, model HostSSHConfigHostResourceModel) (hostSSHConfigHostSpec, diag.Diagnostics) {
	return hostSSHConfigHostSpecFromModelForHome(ctx, model, "")
}

func hostSSHConfigHostSpecFromModelForHome(ctx context.Context, model HostSSHConfigHostResourceModel, homeDir string) (hostSSHConfigHostSpec, diag.Diagnostics) {
	var diags diag.Diagnostics

	configPath := defaultSSHConfigPath
	if !model.ConfigPath.IsNull() && !model.ConfigPath.IsUnknown() {
		configPath = model.ConfigPath.ValueString()
	}
	configPathResolved, err := resolveSSHConfigPathForHome(configPath, homeDir)
	if err != nil {
		diags.AddError("Invalid SSH config path", err.Error())
		return hostSSHConfigHostSpec{}, diags
	}

	if model.Host.IsNull() || model.Host.IsUnknown() {
		diags.AddError("Invalid SSH config host", "host must be known")
		return hostSSHConfigHostSpec{}, diags
	}
	host := model.Host.ValueString()
	if err := validateSSHConfigHostPattern(host); err != nil {
		diags.AddError("Invalid SSH config host", err.Error())
		return hostSSHConfigHostSpec{}, diags
	}

	extraOptions, extraDiags := stringMapValue(ctx, model.ExtraOptions, "SSH config extra_options")
	diags.Append(extraDiags...)
	if diags.HasError() {
		return hostSSHConfigHostSpec{}, diags
	}

	spec := hostSSHConfigHostSpec{
		ID:                 hostSSHConfigHostID(configPathResolved, host),
		ConfigPath:         configPath,
		ConfigPathResolved: configPathResolved,
		Host:               host,
		ExtraOptions:       extraOptions,
	}
	if !model.HostName.IsNull() && !model.HostName.IsUnknown() {
		spec.HostName = model.HostName.ValueString()
	}
	if !model.User.IsNull() && !model.User.IsUnknown() {
		spec.User = model.User.ValueString()
	}
	if !model.Port.IsNull() && !model.Port.IsUnknown() {
		spec.Port = model.Port.ValueInt64()
		if spec.Port < 1 || spec.Port > 65535 {
			diags.AddError("Invalid SSH config port", "port must be between 1 and 65535")
			return hostSSHConfigHostSpec{}, diags
		}
	}
	if !model.IdentityFile.IsNull() && !model.IdentityFile.IsUnknown() {
		spec.IdentityFile = model.IdentityFile.ValueString()
		resolved, err := resolveSSHConfigIdentityFileForHome(spec.IdentityFile, homeDir)
		if err != nil {
			diags.AddError("Invalid SSH config identity_file", err.Error())
			return hostSSHConfigHostSpec{}, diags
		}
		spec.IdentityFileResolved = resolved
	}
	if !model.IdentitiesOnly.IsNull() && !model.IdentitiesOnly.IsUnknown() {
		value := model.IdentitiesOnly.ValueBool()
		spec.IdentitiesOnly = &value
	}
	if !model.ForwardAgent.IsNull() && !model.ForwardAgent.IsUnknown() {
		value := model.ForwardAgent.ValueBool()
		spec.ForwardAgent = &value
	}
	if !model.ProxyJump.IsNull() && !model.ProxyJump.IsUnknown() {
		spec.ProxyJump = model.ProxyJump.ValueString()
	}
	if !model.ProxyCommand.IsNull() && !model.ProxyCommand.IsUnknown() {
		spec.ProxyCommand = model.ProxyCommand.ValueString()
	}
	if !model.AdoptExisting.IsNull() && !model.AdoptExisting.IsUnknown() {
		spec.AdoptExisting = model.AdoptExisting.ValueBool()
	}
	if err := validateSSHConfigHostSpec(spec); err != nil {
		diags.AddError("Invalid SSH config host", err.Error())
		return hostSSHConfigHostSpec{}, diags
	}

	return spec, diags
}

func hostSSHConfigPlanReady(model HostSSHConfigHostResourceModel) bool {
	return !model.ConfigPath.IsUnknown() &&
		!model.Host.IsNull() && !model.Host.IsUnknown() &&
		!model.HostName.IsUnknown() &&
		!model.User.IsUnknown() &&
		!model.Port.IsUnknown() &&
		!model.IdentityFile.IsUnknown() &&
		!model.IdentitiesOnly.IsUnknown() &&
		!model.ForwardAgent.IsUnknown() &&
		!model.ProxyJump.IsUnknown() &&
		!model.ProxyCommand.IsUnknown() &&
		!model.AdoptExisting.IsUnknown() &&
		!model.ExtraOptions.IsUnknown()
}

func renderHostSSHConfigBlock(spec hostSSHConfigHostSpec) string {
	lines := []string{"Host " + spec.Host}
	if spec.HostName != "" {
		lines = append(lines, "  HostName "+sshConfigTokenValue(spec.HostName))
	}
	if spec.User != "" {
		lines = append(lines, "  User "+sshConfigTokenValue(spec.User))
	}
	if spec.Port > 0 {
		lines = append(lines, fmt.Sprintf("  Port %d", spec.Port))
	}
	if spec.IdentityFile != "" {
		lines = append(lines, "  IdentityFile "+sshConfigTokenValue(spec.IdentityFile))
	}
	if spec.IdentitiesOnly != nil {
		lines = append(lines, "  IdentitiesOnly "+sshConfigBoolValue(*spec.IdentitiesOnly))
	}
	if spec.ForwardAgent != nil {
		lines = append(lines, "  ForwardAgent "+sshConfigBoolValue(*spec.ForwardAgent))
	}
	if spec.ProxyJump != "" {
		lines = append(lines, "  ProxyJump "+sshConfigTokenValue(spec.ProxyJump))
	}
	if spec.ProxyCommand != "" {
		lines = append(lines, "  ProxyCommand "+spec.ProxyCommand)
	}

	extraKeys := make([]string, 0, len(spec.ExtraOptions))
	for key := range spec.ExtraOptions {
		extraKeys = append(extraKeys, key)
	}
	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		lines = append(lines, "  "+key+" "+spec.ExtraOptions[key])
	}

	return strings.Join(lines, "\n") + "\n"
}

func withLockedSSHConfigForHome(ctx context.Context, homeDir string, path string, fn func(path string) error) error {
	resolvedPath, err := resolveSSHConfigPathForHome(path, homeDir)
	if err != nil {
		return err
	}

	lock, err := lockHostFile(resolvedPath)
	if err != nil {
		return err
	}
	defer lock.close()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return fn(resolvedPath)
}

func upsertHostSSHConfigManagedBlock(path string, blockID string, host string, renderedBlock string, adoptExisting bool) error {
	content, err := readSSHConfigFile(path)
	if err != nil {
		return err
	}
	lines := splitHostFileLines(content)
	start, end, exists, err := findHostSSHConfigManagedBlockRange(lines, blockID)
	if err != nil {
		return err
	}
	if !exists && !adoptExisting && unmanagedSSHConfigHostExists(lines, host) {
		return fmt.Errorf("SSH config %q already contains unmanaged Host %q; remove it or manage a different alias", path, host)
	}

	blockLines := splitHostFileLines(renderHostSSHConfigManagedBlock(blockID, renderedBlock))
	if exists {
		lines = replaceLines(lines, start, end+1, blockLines)
	} else if adoptExisting {
		unmanagedStart, unmanagedEnd, hasUnmanaged := findUnmanagedSSHConfigHostBlockRange(lines, host)
		if hasUnmanaged {
			lines = replaceLines(lines, unmanagedStart, unmanagedEnd, blockLines)
		} else {
			if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
				lines = append(lines, "\n")
			}
			lines = append(lines, blockLines...)
		}
	} else {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "\n")
		}
		lines = append(lines, blockLines...)
	}

	return writeSSHConfigFile(path, strings.Join(lines, ""))
}

func findUnmanagedSSHConfigHostBlockRange(lines []string, host string) (int, int, bool) {
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lineBody(lines[i]))
		blockID, ok := parseHostSSHConfigBlockBegin(line)
		if ok {
			end := findMarkerLine(lines, i+1, hostSSHConfigBlockEndMarker(blockID))
			if end == -1 {
				return 0, 0, false
			}
			i = end
			continue
		}
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "Host") {
			continue
		}
		matches := false
		for _, pattern := range fields[1:] {
			if pattern == host {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}

		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			nextLine := strings.TrimSpace(lineBody(lines[j]))
			if strings.HasPrefix(nextLine, hostSSHConfigBlockBeginPrefix) {
				end = j
				break
			}
			nextFields := strings.Fields(nextLine)
			if len(nextFields) >= 2 && strings.EqualFold(nextFields[0], "Host") {
				end = j
				break
			}
		}

		return i, end, true
	}
	return 0, 0, false
}

func readHostSSHConfigManagedBlock(path string, blockID string) (string, bool, error) {
	content, err := readSSHConfigFileIfExists(path)
	if err != nil {
		return "", false, err
	}
	if content == "" {
		return "", false, nil
	}

	lines := splitHostFileLines(content)
	start, end, exists, err := findHostSSHConfigManagedBlockRange(lines, blockID)
	if err != nil || !exists {
		return "", exists, err
	}

	return strings.Join(lines[start+1:end], ""), true, nil
}

func removeHostSSHConfigManagedBlock(path string, blockID string) error {
	return removeHostSSHConfigHostBlock(path, blockID, "", false)
}

func readUnmanagedSSHConfigHostBlock(path string, host string) (string, bool, error) {
	content, err := readSSHConfigFileIfExists(path)
	if err != nil || content == "" {
		return "", false, err
	}

	lines := splitHostFileLines(content)
	start, end, exists := findUnmanagedSSHConfigHostBlockRange(lines, host)
	if !exists {
		return "", false, nil
	}

	return strings.TrimRight(strings.Join(lines[start:end], ""), "\n") + "\n", true, nil
}

func removeHostSSHConfigHostBlock(path string, blockID string, host string, adoptExisting bool) error {
	content, err := readSSHConfigFileIfExists(path)
	if err != nil || content == "" {
		return err
	}

	lines := splitHostFileLines(content)
	start, end, exists, err := findHostSSHConfigManagedBlockRange(lines, blockID)
	if err != nil {
		return err
	}
	endExclusive := end + 1
	if !exists && adoptExisting && host != "" {
		start, end, exists = findUnmanagedSSHConfigHostBlockRange(lines, host)
		endExclusive = end
	}
	if !exists {
		return nil
	}
	lines = replaceLines(lines, start, endExclusive, nil)

	return writeSSHConfigFile(path, compactSSHConfigBlankLines(strings.Join(lines, "")))
}

func readSSHConfigFile(path string) (string, error) {
	content, err := readSSHConfigFileIfExists(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), hostSSHConfigDefaultDirPermission); err != nil {
		return "", fmt.Errorf("create SSH config parent directory for %q: %w", path, err)
	}
	return content, nil
}

func readSSHConfigFileIfExists(path string) (string, error) {
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read SSH config %q: %w", path, err)
	}
	return string(content), nil
}

func writeSSHConfigFile(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), hostSSHConfigDefaultDirPermission); err != nil {
		return fmt.Errorf("create SSH config parent directory for %q: %w", path, err)
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), hostSSHConfigDefaultFilePermission); err != nil {
		return fmt.Errorf("write SSH config %q: %w", path, err)
	}
	return nil
}

func findHostSSHConfigManagedBlockRange(lines []string, blockID string) (int, int, bool, error) {
	start := findMarkerLine(lines, 0, hostSSHConfigBlockBeginMarker(blockID))
	if start == -1 {
		return 0, 0, false, nil
	}
	end := findMarkerLine(lines, start+1, hostSSHConfigBlockEndMarker(blockID))
	if end == -1 {
		return 0, 0, false, fmt.Errorf("managed SSH config host %q is missing its end marker", blockID)
	}
	return start, end, true, nil
}

func renderHostSSHConfigManagedBlock(blockID string, renderedBlock string) string {
	var builder strings.Builder
	builder.WriteString(hostSSHConfigBlockBeginMarker(blockID))
	builder.WriteString("\n")
	builder.WriteString(renderedBlock)
	if renderedBlock != "" && !strings.HasSuffix(renderedBlock, "\n") {
		builder.WriteString("\n")
	}
	builder.WriteString(hostSSHConfigBlockEndMarker(blockID))
	builder.WriteString("\n")
	return builder.String()
}

func unmanagedSSHConfigHostExists(lines []string, host string) bool {
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lineBody(lines[i]))
		blockID, ok := parseHostSSHConfigBlockBegin(line)
		if ok {
			end := findMarkerLine(lines, i+1, hostSSHConfigBlockEndMarker(blockID))
			if end == -1 {
				return false
			}
			i = end
			continue
		}
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "Host") {
			continue
		}
		for _, pattern := range fields[1:] {
			if pattern == host {
				return true
			}
		}
	}
	return false
}

func compactSSHConfigBlankLines(content string) string {
	lines := splitHostFileLines(content)
	next := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		isBlank := strings.TrimSpace(line) == ""
		if isBlank && blank {
			continue
		}
		next = append(next, line)
		blank = isBlank
	}
	return strings.TrimLeft(strings.Join(next, ""), "\n")
}

func hostSSHConfigBlockBeginMarker(blockID string) string {
	return hostSSHConfigBlockBeginPrefix + blockID
}

func hostSSHConfigBlockEndMarker(blockID string) string {
	return hostSSHConfigBlockEndPrefix + blockID
}

func parseHostSSHConfigBlockBegin(line string) (string, bool) {
	if !strings.HasPrefix(line, hostSSHConfigBlockBeginPrefix) {
		return "", false
	}
	return strings.TrimPrefix(line, hostSSHConfigBlockBeginPrefix), true
}

func parseHostSSHConfigHostImportID(importID string) (string, string, error) {
	importID = strings.TrimSpace(importID)
	if importID == "" {
		return "", "", fmt.Errorf("import ID must be `host` or `config_path,host`")
	}

	configPath := defaultSSHConfigPath
	host := importID
	if left, right, ok := strings.Cut(importID, ","); ok {
		configPath = strings.TrimSpace(left)
		host = strings.TrimSpace(right)
		if configPath == "" || host == "" {
			return "", "", fmt.Errorf("import ID must be `host` or `config_path,host`")
		}
	}
	if err := validateSSHConfigHostPattern(host); err != nil {
		return "", "", err
	}
	return configPath, host, nil
}

func hostSSHConfigHostID(configPathResolved string, host string) string {
	sum := sha256.Sum256([]byte(configPathResolved + "\x00" + host))
	return hex.EncodeToString(sum[:16])
}

func resolveSSHConfigPathForHome(path string, homeDir string) (string, error) {
	if strings.Contains(path, "\x00") {
		return "", fmt.Errorf("path must not contain NUL bytes")
	}
	return expandHostPathForHome(path, homeDir)
}

func resolveSSHConfigIdentityFileForHome(path string, homeDir string) (string, error) {
	if strings.Contains(path, "\x00") {
		return "", fmt.Errorf("identity_file must not contain NUL bytes")
	}
	return expandHostPathForHome(path, homeDir)
}

func validateSSHConfigHostSpec(spec hostSSHConfigHostSpec) error {
	if spec.HostName != "" {
		if err := validateSSHConfigTokenValue("hostname", spec.HostName); err != nil {
			return err
		}
	}
	if spec.User != "" {
		if err := validateSSHConfigTokenValue("user", spec.User); err != nil {
			return err
		}
	}
	if spec.Port < 0 || spec.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if spec.IdentityFile != "" {
		if err := validateSSHConfigLineValue("identity_file", spec.IdentityFile); err != nil {
			return err
		}
	}
	if spec.ProxyJump != "" {
		if err := validateSSHConfigTokenValue("proxy_jump", spec.ProxyJump); err != nil {
			return err
		}
	}
	if spec.ProxyCommand != "" {
		if err := validateSSHConfigLineValue("proxy_command", spec.ProxyCommand); err != nil {
			return err
		}
	}
	return validateSSHConfigExtraOptions(spec.ExtraOptions)
}

func validateSSHConfigHostPattern(host string) error {
	if strings.TrimSpace(host) != host || host == "" {
		return fmt.Errorf("host must be non-empty and must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(host, " \t\r\n\x00") {
		return fmt.Errorf("host %q is invalid; this resource manages a single Host pattern, so whitespace is not allowed", host)
	}
	return nil
}

func validateSSHConfigTokenValue(name string, value string) error {
	if strings.TrimSpace(value) != value || value == "" {
		return fmt.Errorf("%s must be non-empty and must not contain leading or trailing whitespace", name)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%s must not contain newlines or NUL bytes", name)
	}
	return nil
}

func validateSSHConfigLineValue(name string, value string) error {
	if strings.TrimSpace(value) != value || value == "" {
		return fmt.Errorf("%s must be non-empty and must not contain leading or trailing whitespace", name)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%s must not contain newlines or NUL bytes", name)
	}
	return nil
}

func validateSSHConfigExtraOptions(options map[string]string) error {
	reserved := map[string]struct{}{
		"host":           {},
		"hostname":       {},
		"user":           {},
		"port":           {},
		"identityfile":   {},
		"identitiesonly": {},
		"forwardagent":   {},
		"proxyjump":      {},
		"proxycommand":   {},
	}
	for key, value := range options {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if key == "" || normalizedKey != strings.ToLower(key) && strings.TrimSpace(key) != key {
			return fmt.Errorf("extra option key %q is invalid; keys must not contain leading or trailing whitespace", key)
		}
		if strings.ContainsAny(key, " \t\r\n\x00") {
			return fmt.Errorf("extra option key %q is invalid; whitespace and NUL bytes are not allowed", key)
		}
		if _, ok := reserved[normalizedKey]; ok {
			return fmt.Errorf("extra option %q is managed by a first-class attribute", key)
		}
		if err := validateSSHConfigLineValue("extra option "+key, value); err != nil {
			return err
		}
	}
	return nil
}

func sshConfigBoolValue(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func sshConfigTokenValue(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t#\"") {
		return strconv.Quote(value)
	}
	return value
}
