---
page_title: "Host Provider: Runtime and Security"
description: |-
  Local command execution, runtime metadata, privileged operations, and the AUR trust boundary.
---

# Runtime and Security

## Local Execution

The provider runs commands on the machine executing Terraform. Required tools vary by resource and include package managers, system-management utilities, `git`, `ssh-keygen`, `crontab`, and macOS command-line tools. Protected operations may authenticate through `sudo`.

`git`, `ssh-keygen`, and AUR helpers are resolved when their operations run. When `aur_helper` is configured, AUR package mutations install missing `base-devel` and `git` prerequisites and bootstrap that helper. Planning, refresh, and data-source reads do not install bootstrap tooling. Pacman itself must already exist before Pacman or AUR objects are configured.

## Packages and Data Sources

Each `host_package_*` backend has a resource for lifecycle ownership and a data source for read-only lookup. AUR data sources use local Pacman state by default and query a verified AUR helper only when `include_remote = true`.

## Privileged Files

`host_system_file` and `host_sudoers_rule` resolve their privileged utility allowlist from protected system directories rather than the caller's `PATH`. Executables and their parent directories must be root-owned and not writable by group or other users.

## Runtime Metadata

User-scoped metadata, including file-block state and schedule scripts, defaults to `~/.local/state/terraform-provider-host` under `target_user`. A system-only provider has no user runtime directory.

When `host_link.stage_source` is enabled, the provider also stores a
content-addressed copy below `runtime_dir/links`. The live symbolic link points
to that stable copy instead of the Terraform working directory. Old staged
versions are removed only after the replacement link has been published.

## AUR Trust Boundary

Provider-configured AUR bootstrap builds `yay` or `paru` from the current AUR Git HEAD. AUR repositories and `PKGBUILD` files are user-contributed and mutable, and builds execute unsandboxed code as the Terraform user.

Review and trust every AUR package before applying it. Verifying that Pacman owns the helper executable does not audit the helper or package source.
