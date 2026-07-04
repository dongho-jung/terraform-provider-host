package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	tfpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"golang.org/x/crypto/ssh"
)

var (
	_ resource.Resource                = &HostSSHKeyResource{}
	_ resource.ResourceWithConfigure   = &HostSSHKeyResource{}
	_ resource.ResourceWithImportState = &HostSSHKeyResource{}
	_ resource.ResourceWithModifyPlan  = &HostSSHKeyResource{}
)

const (
	hostSSHKeyTypeEd25519 = "ed25519"
	hostSSHKeyTypeRSA     = "rsa"
	hostSSHKeyTypeECDSA   = "ecdsa"
)

type HostSSHKeyResource struct {
	manager SSHKeyManager
}

type HostSSHKeyResourceModel struct {
	ID                    types.String `tfsdk:"id"`
	Path                  types.String `tfsdk:"path"`
	PathResolved          types.String `tfsdk:"path_resolved"`
	Type                  types.String `tfsdk:"type"`
	Bits                  types.Int64  `tfsdk:"bits"`
	Comment               types.String `tfsdk:"comment"`
	DeleteOnDestroy       types.Bool   `tfsdk:"delete_on_destroy"`
	PublicKey             types.String `tfsdk:"public_key"`
	PublicKeyPath         types.String `tfsdk:"public_key_path"`
	PublicKeyPathResolved types.String `tfsdk:"public_key_path_resolved"`
	FingerprintSHA256     types.String `tfsdk:"fingerprint_sha256"`
}

type SSHKeyManager interface {
	KeyStatus(ctx context.Context, path string) (HostSSHKeyStatus, bool, error)
	EnsureKey(ctx context.Context, spec HostSSHKeySpec) (HostSSHKeyStatus, error)
	DeleteKey(ctx context.Context, path string) error
}

type HostSSHKeySpec struct {
	Path    string
	Type    string
	Bits    int64
	Comment string
}

type HostSSHKeyStatus struct {
	Path                  string
	PublicKeyPath         string
	Type                  string
	Comment               string
	PublicKey             string
	FingerprintSHA256     string
	PathResolved          string
	PublicKeyPathResolved string
}

type CLISSHKeyManager struct {
	sshKeygenPath string
}

func NewCLISSHKeyManager(sshKeygenPath string) SSHKeyManager {
	return &CLISSHKeyManager{
		sshKeygenPath: sshKeygenPath,
	}
}

func NewHostSSHKeyResource() resource.Resource {
	return &HostSSHKeyResource{}
}

func (r *HostSSHKeyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (r *HostSSHKeyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.SSHKeyManager == nil {
			resp.Diagnostics.AddError("ssh-keygen executable not found", "`host_ssh_key` requires `ssh-keygen` to be available in PATH.")
			return
		}
		r.manager = data.SSHKeyManager
	case SSHKeyManager:
		r.manager = data
	default:
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected HostProviderData or SSHKeyManager, got %T.", req.ProviderData))
	}
}

func (r *HostSSHKeyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates or adopts a local SSH keypair without storing private key material in Terraform state.",
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
				MarkdownDescription: "Private key path. `~` is expanded to the current user's home directory and relative paths are resolved from the Terraform working directory.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"path_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute private key path.",
			},
			"type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "SSH key type. Supported values are `ed25519`, `rsa`, and `ecdsa`. Defaults to `ed25519` when creating a new key.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"bits": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Key size passed to `ssh-keygen -b`. For RSA use at least 2048. For ECDSA use 256, 384, or 521. Omit for Ed25519.",
			},
			"comment": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Public key comment. Defaults to an empty comment when creating a new key; adopted keys keep their current public key comment.",
			},
			"delete_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Delete the private and public key files when destroying the resource. Defaults to false.",
			},
			"public_key": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Authorized-key formatted public key. This is not secret.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"public_key_path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Public key path, normally `path` with `.pub` appended.",
			},
			"public_key_path_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute public key path.",
			},
			"fingerprint_sha256": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "OpenSSH SHA256 fingerprint for the public key.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *HostSSHKeyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostSSHKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Path.IsNull() || plan.Path.IsUnknown() {
		return
	}

	pathResolved, err := resolveSSHKeyPath(plan.Path.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid SSH key path", err.Error())
		return
	}
	if !plan.Type.IsNull() && !plan.Type.IsUnknown() {
		if _, err := normalizeSSHKeyType(plan.Type.ValueString()); err != nil {
			resp.Diagnostics.AddError("Invalid SSH key type", err.Error())
			return
		}
	}
	if !plan.Bits.IsNull() && !plan.Bits.IsUnknown() {
		keyType := hostSSHKeyTypeEd25519
		if !plan.Type.IsNull() && !plan.Type.IsUnknown() {
			keyType = plan.Type.ValueString()
		}
		if err := validateSSHKeyBits(keyType, plan.Bits.ValueInt64()); err != nil {
			resp.Diagnostics.AddError("Invalid SSH key bits", err.Error())
			return
		}
	}
	if !plan.Comment.IsNull() && !plan.Comment.IsUnknown() {
		if err := validateSSHKeyComment(plan.Comment.ValueString()); err != nil {
			resp.Diagnostics.AddError("Invalid SSH key comment", err.Error())
			return
		}
	}

	plan.ID = types.StringValue(plan.Path.ValueString())
	plan.PathResolved = types.StringValue(pathResolved)
	plan.PublicKeyPath = types.StringValue(publicSSHKeyPath(plan.Path.ValueString()))
	plan.PublicKeyPathResolved = types.StringValue(publicSSHKeyPath(pathResolved))
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostSSHKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostSSHKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncKey(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync SSH key", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSSHKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostSSHKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if r.manager == nil {
		resp.Diagnostics.AddError("SSH key manager unavailable", "`host_ssh_key` requires `ssh-keygen` to be available in PATH.")
		return
	}

	pathResolved, err := resolveSSHKeyPath(state.Path.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid SSH key path", err.Error())
		return
	}
	status, exists, err := r.manager.KeyStatus(ctx, pathResolved)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read SSH key", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	next := hydrateSSHKeyModel(state, status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostSSHKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostSSHKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncKey(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync SSH key", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostSSHKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostSSHKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.DeleteOnDestroy.IsNull() || state.DeleteOnDestroy.IsUnknown() || !state.DeleteOnDestroy.ValueBool() {
		return
	}
	if r.manager == nil {
		resp.Diagnostics.AddError("SSH key manager unavailable", "`host_ssh_key` requires `ssh-keygen` to be available in PATH.")
		return
	}

	pathResolved, err := resolveSSHKeyPath(state.Path.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid SSH key path", err.Error())
		return
	}
	if err := r.manager.DeleteKey(ctx, pathResolved); err != nil {
		resp.Diagnostics.AddError("Failed to delete SSH key", err.Error())
	}
}

func (r *HostSSHKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, tfpath.Root("path"), req, resp)
}

func (r *HostSSHKeyResource) syncKey(ctx context.Context, model HostSSHKeyResourceModel) (HostSSHKeyResourceModel, error) {
	if r.manager == nil {
		return model, fmt.Errorf("ssh-keygen executable not found")
	}

	pathResolved, err := resolveSSHKeyPath(model.Path.ValueString())
	if err != nil {
		return model, err
	}

	status, exists, err := r.manager.KeyStatus(ctx, pathResolved)
	if err != nil {
		return model, err
	}
	if exists {
		if !model.Type.IsNull() && !model.Type.IsUnknown() {
			wantType, err := normalizeSSHKeyType(model.Type.ValueString())
			if err != nil {
				return model, err
			}
			if status.Type != wantType {
				return model, fmt.Errorf("existing SSH key at %q has type %q, not %q", pathResolved, status.Type, wantType)
			}
		}
		if !model.Comment.IsNull() && !model.Comment.IsUnknown() && model.Comment.ValueString() != status.Comment {
			return model, fmt.Errorf("existing SSH key at %q has public key comment %q, not %q", pathResolved, status.Comment, model.Comment.ValueString())
		}
		return hydrateSSHKeyModel(model, status), nil
	}

	spec, err := sshKeySpecFromModel(model)
	if err != nil {
		return model, err
	}
	status, err = r.manager.EnsureKey(ctx, spec)
	if err != nil {
		return model, err
	}

	return hydrateSSHKeyModel(model, status), nil
}

func (m *CLISSHKeyManager) KeyStatus(ctx context.Context, path string) (HostSSHKeyStatus, bool, error) {
	privatePath, err := resolveSSHKeyPath(path)
	if err != nil {
		return HostSSHKeyStatus{}, false, err
	}
	if _, err := os.Stat(privatePath); err != nil {
		if os.IsNotExist(err) {
			return HostSSHKeyStatus{}, false, nil
		}
		return HostSSHKeyStatus{}, false, fmt.Errorf("read private key %q: %w", privatePath, err)
	}

	publicKeyPath := publicSSHKeyPath(privatePath)
	publicKey, err := m.readPublicKey(ctx, publicKeyPath, privatePath)
	if err != nil {
		return HostSSHKeyStatus{}, false, err
	}
	status, err := sshKeyStatusFromPublicKey(privatePath, publicKeyPath, publicKey)
	if err != nil {
		return HostSSHKeyStatus{}, false, err
	}

	return status, true, nil
}

func (m *CLISSHKeyManager) EnsureKey(ctx context.Context, spec HostSSHKeySpec) (HostSSHKeyStatus, error) {
	privatePath, err := resolveSSHKeyPath(spec.Path)
	if err != nil {
		return HostSSHKeyStatus{}, err
	}
	spec.Path = privatePath
	if spec.Type == "" {
		spec.Type = hostSSHKeyTypeEd25519
	}
	keyType, err := normalizeSSHKeyType(spec.Type)
	if err != nil {
		return HostSSHKeyStatus{}, err
	}
	spec.Type = keyType
	if err := validateSSHKeyBits(spec.Type, spec.Bits); err != nil {
		return HostSSHKeyStatus{}, err
	}
	if err := validateSSHKeyComment(spec.Comment); err != nil {
		return HostSSHKeyStatus{}, err
	}

	if status, exists, err := m.KeyStatus(ctx, privatePath); err != nil || exists {
		return status, err
	}
	if err := m.generateKey(ctx, spec); err != nil {
		return HostSSHKeyStatus{}, err
	}

	status, exists, err := m.KeyStatus(ctx, privatePath)
	if err != nil {
		return HostSSHKeyStatus{}, err
	}
	if !exists {
		return HostSSHKeyStatus{}, fmt.Errorf("ssh-keygen did not create %q", privatePath)
	}
	return status, nil
}

func (m *CLISSHKeyManager) DeleteKey(ctx context.Context, path string) error {
	privatePath, err := resolveSSHKeyPath(path)
	if err != nil {
		return err
	}
	if err := os.Remove(privatePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove private key %q: %w", privatePath, err)
	}
	publicPath := publicSSHKeyPath(privatePath)
	if err := os.Remove(publicPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove public key %q: %w", publicPath, err)
	}
	return nil
}

func (m *CLISSHKeyManager) generateKey(ctx context.Context, spec HostSSHKeySpec) error {
	parent := filepath.Dir(spec.Path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create SSH key parent directory %q: %w", parent, err)
	}

	args := []string{"-q", "-t", spec.Type, "-f", spec.Path, "-N", "", "-C", spec.Comment}
	if spec.Bits > 0 {
		args = append(args, "-b", strconv.FormatInt(spec.Bits, 10))
	}

	cmd := exec.CommandContext(ctx, m.sshKeygenPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("run ssh-keygen %s: %w%s", strings.Join(args, " "), err, commandOutputSuffix(out))
	}

	if err := os.Chmod(spec.Path, 0o600); err != nil {
		return fmt.Errorf("chmod private key %q: %w", spec.Path, err)
	}
	publicPath := publicSSHKeyPath(spec.Path)
	if err := os.Chmod(publicPath, 0o644); err != nil {
		return fmt.Errorf("chmod public key %q: %w", publicPath, err)
	}

	return nil
}

func (m *CLISSHKeyManager) readPublicKey(ctx context.Context, publicKeyPath string, privateKeyPath string) (string, error) {
	content, err := os.ReadFile(publicKeyPath)
	if err == nil {
		return strings.TrimSpace(string(content)), nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read public key %q: %w", publicKeyPath, err)
	}

	cmd := exec.CommandContext(ctx, m.sshKeygenPath, "-y", "-f", privateKeyPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("derive public key from %q: %w%s", privateKeyPath, err, commandOutputSuffix(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func sshKeySpecFromModel(model HostSSHKeyResourceModel) (HostSSHKeySpec, error) {
	path, err := resolveSSHKeyPath(model.Path.ValueString())
	if err != nil {
		return HostSSHKeySpec{}, err
	}

	keyType := hostSSHKeyTypeEd25519
	if !model.Type.IsNull() && !model.Type.IsUnknown() {
		keyType, err = normalizeSSHKeyType(model.Type.ValueString())
		if err != nil {
			return HostSSHKeySpec{}, err
		}
	}

	var bits int64
	if !model.Bits.IsNull() && !model.Bits.IsUnknown() {
		bits = model.Bits.ValueInt64()
	}
	if err := validateSSHKeyBits(keyType, bits); err != nil {
		return HostSSHKeySpec{}, err
	}

	comment := ""
	if !model.Comment.IsNull() && !model.Comment.IsUnknown() {
		comment = model.Comment.ValueString()
	}
	if err := validateSSHKeyComment(comment); err != nil {
		return HostSSHKeySpec{}, err
	}

	return HostSSHKeySpec{
		Path:    path,
		Type:    keyType,
		Bits:    bits,
		Comment: comment,
	}, nil
}

func hydrateSSHKeyModel(model HostSSHKeyResourceModel, status HostSSHKeyStatus) HostSSHKeyResourceModel {
	model.ID = types.StringValue(model.Path.ValueString())
	model.PathResolved = types.StringValue(status.PathResolved)
	model.Type = types.StringValue(status.Type)
	model.Comment = types.StringValue(status.Comment)
	model.PublicKey = types.StringValue(status.PublicKey)
	model.PublicKeyPath = types.StringValue(publicSSHKeyPath(model.Path.ValueString()))
	model.PublicKeyPathResolved = types.StringValue(status.PublicKeyPathResolved)
	model.FingerprintSHA256 = types.StringValue(status.FingerprintSHA256)
	return model
}

func sshKeyStatusFromPublicKey(privatePath string, publicPath string, publicKey string) (HostSSHKeyStatus, error) {
	parsedKey, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKey + "\n"))
	if err != nil {
		return HostSSHKeyStatus{}, fmt.Errorf("parse public key %q: %w", publicPath, err)
	}
	keyType, err := sshKeyTypeFromPublicKeyType(parsedKey.Type())
	if err != nil {
		return HostSSHKeyStatus{}, err
	}

	return HostSSHKeyStatus{
		Path:                  privatePath,
		PublicKeyPath:         publicPath,
		Type:                  keyType,
		Comment:               comment,
		PublicKey:             strings.TrimSpace(publicKey),
		FingerprintSHA256:     ssh.FingerprintSHA256(parsedKey),
		PathResolved:          privatePath,
		PublicKeyPathResolved: publicPath,
	}, nil
}

func resolveSSHKeyPath(path string) (string, error) {
	if strings.Contains(path, "\x00") {
		return "", fmt.Errorf("path must not contain NUL bytes")
	}
	return expandHostPath(path)
}

func publicSSHKeyPath(path string) string {
	return path + ".pub"
}

func normalizeSSHKeyType(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case hostSSHKeyTypeEd25519, hostSSHKeyTypeRSA, hostSSHKeyTypeECDSA:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported SSH key type %q; expected ed25519, rsa, or ecdsa", value)
	}
}

func sshKeyTypeFromPublicKeyType(value string) (string, error) {
	switch {
	case value == ssh.KeyAlgoED25519:
		return hostSSHKeyTypeEd25519, nil
	case value == ssh.KeyAlgoRSA:
		return hostSSHKeyTypeRSA, nil
	case strings.HasPrefix(value, "ecdsa-sha2-"):
		return hostSSHKeyTypeECDSA, nil
	default:
		return "", fmt.Errorf("unsupported SSH public key type %q", value)
	}
}

func validateSSHKeyBits(keyType string, bits int64) error {
	if bits == 0 {
		return nil
	}
	if bits < 0 {
		return fmt.Errorf("bits must be positive")
	}

	normalized, err := normalizeSSHKeyType(keyType)
	if err != nil {
		return err
	}
	switch normalized {
	case hostSSHKeyTypeEd25519:
		return fmt.Errorf("bits is not supported for ed25519 keys")
	case hostSSHKeyTypeRSA:
		if bits < 2048 {
			return fmt.Errorf("rsa bits must be at least 2048")
		}
	case hostSSHKeyTypeECDSA:
		if bits != 256 && bits != 384 && bits != 521 {
			return fmt.Errorf("ecdsa bits must be one of 256, 384, or 521")
		}
	}
	return nil
}

func validateSSHKeyComment(comment string) error {
	if strings.ContainsAny(comment, "\r\n\x00") {
		return fmt.Errorf("comment must not contain newlines or NUL bytes")
	}
	return nil
}

func commandOutputSuffix(out []byte) string {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return ""
	}
	return ": " + string(out)
}
