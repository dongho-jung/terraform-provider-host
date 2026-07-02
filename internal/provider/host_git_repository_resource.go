package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &HostGitRepositoryResource{}
	_ resource.ResourceWithConfigure   = &HostGitRepositoryResource{}
	_ resource.ResourceWithImportState = &HostGitRepositoryResource{}
	_ resource.ResourceWithModifyPlan  = &HostGitRepositoryResource{}
)

var gitSHAishPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

type HostGitRepositoryResource struct {
	gitPath string
}

type HostGitRepositoryResourceModel struct {
	ID              types.String `tfsdk:"id"`
	URL             types.String `tfsdk:"url"`
	Path            types.String `tfsdk:"path"`
	PathResolved    types.String `tfsdk:"path_resolved"`
	Ref             types.String `tfsdk:"ref"`
	RemoteName      types.String `tfsdk:"remote_name"`
	TrackRemote     types.Bool   `tfsdk:"track_remote"`
	Recursive       types.Bool   `tfsdk:"recursive"`
	Force           types.Bool   `tfsdk:"force"`
	DeleteOnDestroy types.Bool   `tfsdk:"delete_on_destroy"`
	Commit          types.String `tfsdk:"commit"`
	RemoteCommit    types.String `tfsdk:"remote_commit"`
	Dirty           types.Bool   `tfsdk:"dirty"`
}

func NewHostGitRepositoryResource() resource.Resource {
	return &HostGitRepositoryResource{}
}

func (r *HostGitRepositoryResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_git_repo"
}

func (r *HostGitRepositoryResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	switch data := req.ProviderData.(type) {
	case HostProviderData:
		if data.GitPath == "" {
			resp.Diagnostics.AddError("Git executable not found", "`host_git_repo` requires `git` to be available in PATH.")
			return
		}
		r.gitPath = data.GitPath
	case string:
		r.gitPath = data
	default:
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected HostProviderData or git path string, got %T.", req.ProviderData))
	}
}

func (r *HostGitRepositoryResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Clones and optionally updates a Git repository at a host path.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, equal to `path`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"url": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Git remote URL, such as an HTTPS URL, SSH URL, or local path accepted by `git clone`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Destination path for the checkout. `~` is expanded to the current user's home directory and relative paths are resolved from the Terraform working directory.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"path_resolved": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved absolute checkout path.",
			},
			"ref": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Branch, tag, or commit to checkout. Omit to use the remote default branch.",
			},
			"remote_name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("origin"),
				MarkdownDescription: "Git remote name. Defaults to `origin`.",
			},
			"track_remote": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "When true, plan and apply fetch the remote and move the checkout to the latest commit for `ref` or the remote default branch.",
			},
			"recursive": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Initialize and update submodules recursively.",
			},
			"force": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Discard local checkout changes when moving to the desired commit. Defaults to false.",
			},
			"delete_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Remove the cloned repository directory on destroy. The provider refuses to remove paths that are not Git repositories.",
			},
			"commit": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Current checked-out commit SHA.",
			},
			"remote_commit": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Latest remote commit SHA for `ref` when `track_remote = true`.",
			},
			"dirty": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the checkout has local modified or untracked files.",
			},
		},
	}
}

func (r *HostGitRepositoryResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan HostGitRepositoryResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !hostGitRepositoryPlanReady(plan) {
		return
	}

	spec, err := hostGitRepositorySpecFromModel(plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Git repository", err.Error())
		return
	}

	plan.ID = types.StringValue(spec.Path)
	plan.PathResolved = types.StringValue(spec.PathResolved)

	if spec.TrackRemote {
		if r.gitPath == "" {
			resp.Diagnostics.AddError("Git executable not found", "`host_git_repo` requires `git` to be available in PATH.")
			return
		}
		remoteCommit, err := gitResolveRemoteRef(ctx, r.gitPath, spec.URL, spec.Ref)
		if err != nil {
			resp.Diagnostics.AddError("Failed to resolve Git remote ref", err.Error())
			return
		}
		plan.RemoteCommit = types.StringValue(remoteCommit)
		plan.Commit = types.StringValue(remoteCommit)
	}

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *HostGitRepositoryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan HostGitRepositoryResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncRepository(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync Git repository", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostGitRepositoryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state HostGitRepositoryResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	next, exists, err := r.readRepository(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Failed to read Git repository", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *HostGitRepositoryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan HostGitRepositoryResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.syncRepository(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Failed to sync Git repository", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostGitRepositoryResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state HostGitRepositoryResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.deleteRepository(ctx, state); err != nil {
		resp.Diagnostics.AddError("Failed to delete Git repository", err.Error())
	}
}

func (r *HostGitRepositoryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	state, err := r.importRepositoryState(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to import Git repository", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *HostGitRepositoryResource) importRepositoryState(ctx context.Context, importID string) (HostGitRepositoryResourceModel, error) {
	if r.gitPath == "" {
		return HostGitRepositoryResourceModel{}, fmt.Errorf("git executable not found in PATH")
	}

	pathValue := strings.TrimSpace(importID)
	if pathValue == "" {
		return HostGitRepositoryResourceModel{}, fmt.Errorf("import ID must be the repository path")
	}
	pathResolved, err := expandHostPath(pathValue)
	if err != nil {
		return HostGitRepositoryResourceModel{}, err
	}
	if exists, err := pathExists(pathResolved); err != nil {
		return HostGitRepositoryResourceModel{}, err
	} else if !exists {
		return HostGitRepositoryResourceModel{}, fmt.Errorf("repository path %q does not exist", pathResolved)
	}

	spec := hostGitRepositorySpec{
		Path:            pathValue,
		PathResolved:    pathResolved,
		RemoteName:      "origin",
		TrackRemote:     false,
		Recursive:       false,
		Force:           false,
		DeleteOnDestroy: false,
	}
	if err := ensureGitRepositoryPath(ctx, r.gitPath, spec.PathResolved); err != nil {
		return HostGitRepositoryResourceModel{}, err
	}

	url, err := gitRemoteURL(ctx, r.gitPath, spec.PathResolved, spec.RemoteName)
	if err != nil {
		return HostGitRepositoryResourceModel{}, err
	}
	commit, err := gitCurrentCommit(ctx, r.gitPath, spec.PathResolved)
	if err != nil {
		return HostGitRepositoryResourceModel{}, err
	}
	dirty, err := gitCheckoutDirty(ctx, r.gitPath, spec.PathResolved)
	if err != nil {
		return HostGitRepositoryResourceModel{}, err
	}

	return HostGitRepositoryResourceModel{
		ID:              types.StringValue(spec.Path),
		URL:             types.StringValue(url),
		Path:            types.StringValue(spec.Path),
		PathResolved:    types.StringValue(spec.PathResolved),
		Ref:             types.StringNull(),
		RemoteName:      types.StringValue(spec.RemoteName),
		TrackRemote:     types.BoolValue(spec.TrackRemote),
		Recursive:       types.BoolValue(spec.Recursive),
		Force:           types.BoolValue(spec.Force),
		DeleteOnDestroy: types.BoolValue(spec.DeleteOnDestroy),
		Commit:          types.StringValue(commit),
		RemoteCommit:    types.StringNull(),
		Dirty:           types.BoolValue(dirty),
	}, nil
}

func (r *HostGitRepositoryResource) syncRepository(ctx context.Context, model HostGitRepositoryResourceModel) (HostGitRepositoryResourceModel, error) {
	if r.gitPath == "" {
		return model, fmt.Errorf("git executable not found in PATH")
	}

	spec, err := hostGitRepositorySpecFromModel(model)
	if err != nil {
		return model, err
	}

	exists, err := pathExists(spec.PathResolved)
	if err != nil {
		return model, err
	}
	if !exists {
		if err := gitClone(ctx, r.gitPath, spec); err != nil {
			return model, err
		}
	} else if empty, err := directoryEmpty(spec.PathResolved); err != nil {
		return model, err
	} else if empty {
		if err := gitClone(ctx, r.gitPath, spec); err != nil {
			return model, err
		}
	} else if err := ensureGitRepositoryMatches(ctx, r.gitPath, spec); err != nil {
		return model, err
	}

	if err := syncGitRepositoryCheckout(ctx, r.gitPath, spec); err != nil {
		return model, err
	}

	state, exists, err := r.readRepository(ctx, model)
	if err != nil {
		return model, err
	}
	if !exists {
		return model, fmt.Errorf("repository %q was not found after sync", spec.PathResolved)
	}
	return state, nil
}

func (r *HostGitRepositoryResource) readRepository(ctx context.Context, model HostGitRepositoryResourceModel) (HostGitRepositoryResourceModel, bool, error) {
	if r.gitPath == "" {
		return model, false, fmt.Errorf("git executable not found in PATH")
	}

	spec, err := hostGitRepositorySpecFromModel(model)
	if err != nil {
		return model, false, err
	}

	exists, err := pathExists(spec.PathResolved)
	if err != nil {
		return model, false, err
	}
	if !exists {
		return model, false, nil
	}
	if err := ensureGitRepositoryMatches(ctx, r.gitPath, spec); err != nil {
		return model, false, err
	}

	commit, err := gitCurrentCommit(ctx, r.gitPath, spec.PathResolved)
	if err != nil {
		return model, false, err
	}
	dirty, err := gitCheckoutDirty(ctx, r.gitPath, spec.PathResolved)
	if err != nil {
		return model, false, err
	}

	model.ID = types.StringValue(spec.Path)
	model.PathResolved = types.StringValue(spec.PathResolved)
	model.RemoteName = types.StringValue(spec.RemoteName)
	model.Commit = types.StringValue(commit)
	model.Dirty = types.BoolValue(dirty)
	if spec.TrackRemote {
		remoteCommit, err := gitResolveRemoteRef(ctx, r.gitPath, spec.URL, spec.Ref)
		if err != nil {
			return model, false, err
		}
		model.RemoteCommit = types.StringValue(remoteCommit)
	} else {
		model.RemoteCommit = types.StringNull()
	}

	return model, true, nil
}

func (r *HostGitRepositoryResource) deleteRepository(ctx context.Context, model HostGitRepositoryResourceModel) error {
	spec, err := hostGitRepositorySpecFromModel(model)
	if err != nil {
		return err
	}
	if !spec.DeleteOnDestroy {
		return nil
	}

	exists, err := pathExists(spec.PathResolved)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if r.gitPath == "" {
		return fmt.Errorf("git executable not found in PATH")
	}
	if err := ensureGitRepositoryMatches(ctx, r.gitPath, spec); err != nil {
		return err
	}

	return os.RemoveAll(spec.PathResolved)
}

type hostGitRepositorySpec struct {
	URL             string
	Path            string
	PathResolved    string
	Ref             string
	RemoteName      string
	TrackRemote     bool
	Recursive       bool
	Force           bool
	DeleteOnDestroy bool
}

func hostGitRepositorySpecFromModel(model HostGitRepositoryResourceModel) (hostGitRepositorySpec, error) {
	if model.URL.IsNull() || model.URL.IsUnknown() {
		return hostGitRepositorySpec{}, fmt.Errorf("url must be known")
	}
	if model.Path.IsNull() || model.Path.IsUnknown() {
		return hostGitRepositorySpec{}, fmt.Errorf("path must be known")
	}
	if model.RemoteName.IsNull() || model.RemoteName.IsUnknown() {
		return hostGitRepositorySpec{}, fmt.Errorf("remote_name must be known")
	}
	if model.TrackRemote.IsNull() || model.TrackRemote.IsUnknown() ||
		model.Recursive.IsNull() || model.Recursive.IsUnknown() ||
		model.Force.IsNull() || model.Force.IsUnknown() ||
		model.DeleteOnDestroy.IsNull() || model.DeleteOnDestroy.IsUnknown() {
		return hostGitRepositorySpec{}, fmt.Errorf("boolean options must be known")
	}

	url := strings.TrimSpace(model.URL.ValueString())
	if url == "" {
		return hostGitRepositorySpec{}, fmt.Errorf("url must be non-empty")
	}
	if strings.ContainsAny(url, "\x00\r\n") {
		return hostGitRepositorySpec{}, fmt.Errorf("url must not contain control characters")
	}

	pathValue := strings.TrimSpace(model.Path.ValueString())
	if pathValue == "" {
		return hostGitRepositorySpec{}, fmt.Errorf("path must be non-empty")
	}
	if strings.Contains(pathValue, "\x00") {
		return hostGitRepositorySpec{}, fmt.Errorf("path must not contain NUL bytes")
	}
	pathResolved, err := expandHostPath(pathValue)
	if err != nil {
		return hostGitRepositorySpec{}, err
	}

	remoteName := strings.TrimSpace(model.RemoteName.ValueString())
	if remoteName == "" || strings.ContainsAny(remoteName, " \t\r\n\x00") || strings.HasPrefix(remoteName, "-") {
		return hostGitRepositorySpec{}, fmt.Errorf("remote_name %q is invalid", remoteName)
	}

	ref := ""
	if !model.Ref.IsNull() && !model.Ref.IsUnknown() {
		ref = strings.TrimSpace(model.Ref.ValueString())
		if strings.ContainsAny(ref, "\x00\r\n") {
			return hostGitRepositorySpec{}, fmt.Errorf("ref must not contain control characters")
		}
	}

	return hostGitRepositorySpec{
		URL:             url,
		Path:            model.Path.ValueString(),
		PathResolved:    pathResolved,
		Ref:             ref,
		RemoteName:      remoteName,
		TrackRemote:     model.TrackRemote.ValueBool(),
		Recursive:       model.Recursive.ValueBool(),
		Force:           model.Force.ValueBool(),
		DeleteOnDestroy: model.DeleteOnDestroy.ValueBool(),
	}, nil
}

func hostGitRepositoryPlanReady(model HostGitRepositoryResourceModel) bool {
	return !model.URL.IsNull() && !model.URL.IsUnknown() &&
		!model.Path.IsNull() && !model.Path.IsUnknown() &&
		!model.Ref.IsUnknown() &&
		!model.RemoteName.IsNull() && !model.RemoteName.IsUnknown() &&
		!model.TrackRemote.IsNull() && !model.TrackRemote.IsUnknown() &&
		!model.Recursive.IsNull() && !model.Recursive.IsUnknown() &&
		!model.Force.IsNull() && !model.Force.IsUnknown() &&
		!model.DeleteOnDestroy.IsNull() && !model.DeleteOnDestroy.IsUnknown()
}

func gitClone(ctx context.Context, gitPath string, spec hostGitRepositorySpec) error {
	parent := filepath.Dir(spec.PathResolved)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create repository parent directory %q: %w", parent, err)
	}

	args := []string{"clone"}
	if spec.Recursive {
		args = append(args, "--recursive")
	}
	args = append(args, spec.URL, spec.PathResolved)
	if _, err := runGit(ctx, gitPath, "", args...); err != nil {
		return err
	}
	return nil
}

func syncGitRepositoryCheckout(ctx context.Context, gitPath string, spec hostGitRepositorySpec) error {
	var target string
	if spec.TrackRemote {
		remoteCommit, err := gitResolveRemoteRef(ctx, gitPath, spec.URL, spec.Ref)
		if err != nil {
			return err
		}
		if _, err := runGit(ctx, gitPath, spec.PathResolved, "fetch", "--tags", spec.RemoteName); err != nil {
			return err
		}
		target = remoteCommit
	} else if spec.Ref != "" {
		target = spec.Ref
	}

	if target != "" {
		current, _ := gitCurrentCommit(ctx, gitPath, spec.PathResolved)
		if current != target {
			dirty, err := gitCheckoutDirty(ctx, gitPath, spec.PathResolved)
			if err != nil {
				return err
			}
			if dirty {
				if !spec.Force {
					return fmt.Errorf("repository %q has local changes; set force = true to discard them before checkout", spec.PathResolved)
				}
				if _, err := runGit(ctx, gitPath, spec.PathResolved, "reset", "--hard"); err != nil {
					return err
				}
				if _, err := runGit(ctx, gitPath, spec.PathResolved, "clean", "-fd"); err != nil {
					return err
				}
			}
		}
		if _, err := runGit(ctx, gitPath, spec.PathResolved, "checkout", "--detach", target); err != nil {
			return err
		}
	}

	if spec.Recursive {
		if _, err := runGit(ctx, gitPath, spec.PathResolved, "submodule", "update", "--init", "--recursive"); err != nil {
			return err
		}
	}

	return nil
}

func ensureGitRepositoryMatches(ctx context.Context, gitPath string, spec hostGitRepositorySpec) error {
	if err := ensureGitRepositoryPath(ctx, gitPath, spec.PathResolved); err != nil {
		return err
	}
	remoteURL, err := gitRemoteURL(ctx, gitPath, spec.PathResolved, spec.RemoteName)
	if err != nil {
		return err
	}
	if remoteURL != spec.URL {
		return fmt.Errorf("repository %q remote %q is %q, want %q", spec.PathResolved, spec.RemoteName, remoteURL, spec.URL)
	}
	return nil
}

func ensureGitRepositoryPath(ctx context.Context, gitPath string, repoPath string) error {
	info, err := os.Lstat(repoPath)
	if err != nil {
		return fmt.Errorf("read repository path %q: %w", repoPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("repository path %q is a symbolic link; refusing to manage it as host_git_repo", repoPath)
	}
	if !info.IsDir() {
		return fmt.Errorf("repository path %q exists and is not a directory", repoPath)
	}
	if _, err := runGit(ctx, gitPath, repoPath, "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("path %q exists and is not a Git repository", repoPath)
	}
	return nil
}

func gitCurrentCommit(ctx context.Context, gitPath string, repoPath string) (string, error) {
	out, err := runGit(ctx, gitPath, repoPath, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitCheckoutDirty(ctx context.Context, gitPath string, repoPath string) (bool, error) {
	out, err := runGit(ctx, gitPath, repoPath, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func gitRemoteURL(ctx context.Context, gitPath string, repoPath string, remoteName string) (string, error) {
	out, err := runGit(ctx, gitPath, repoPath, "remote", "get-url", remoteName)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitResolveRemoteRef(ctx context.Context, gitPath string, url string, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if gitSHAishPattern.MatchString(ref) {
		return ref, nil
	}

	args := []string{"ls-remote", url}
	if ref == "" {
		args = append(args, "HEAD")
	} else {
		args = append(args, ref)
	}
	out, err := runGit(ctx, gitPath, "", args...)
	if err != nil {
		return "", err
	}
	commit, ok := selectGitRemoteRefCommit(string(out), ref)
	if !ok {
		if ref == "" {
			return "", fmt.Errorf("remote %q did not report HEAD", url)
		}
		return "", fmt.Errorf("remote %q did not report ref %q", url, ref)
	}
	return commit, nil
}

func selectGitRemoteRefCommit(out string, ref string) (string, bool) {
	type remoteRef struct {
		commit string
		name   string
	}
	var refs []remoteRef
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		refs = append(refs, remoteRef{commit: fields[0], name: fields[1]})
	}
	if len(refs) == 0 {
		return "", false
	}
	if ref == "" {
		for _, item := range refs {
			if item.name == "HEAD" {
				return item.commit, true
			}
		}
		return refs[0].commit, true
	}

	preferred := []string{
		"refs/heads/" + ref,
		"refs/tags/" + ref + "^{}",
		"refs/tags/" + ref,
		ref,
	}
	for _, name := range preferred {
		for _, item := range refs {
			if item.name == name {
				return item.commit, true
			}
		}
	}
	return refs[0].commit, true
}

func runGit(ctx context.Context, gitPath string, workDir string, args ...string) ([]byte, error) {
	commandArgs := args
	if workDir != "" {
		commandArgs = append([]string{"-C", workDir}, args...)
	}

	cmd := exec.CommandContext(ctx, gitPath, commandArgs...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s failed: %w\n%s", gitPath, strings.Join(commandArgs, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("read path %q: %w", path, err)
}

func directoryEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("read directory %q: %w", path, err)
	}
	return len(entries) == 0, nil
}
