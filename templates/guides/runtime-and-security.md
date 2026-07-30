---
page_title: "Host Provider: Runtime and Security"
description: |-
  Local command execution, runtime metadata, privileged operations, and the AUR trust boundary.
---

# Runtime and Security

## Local Execution

The provider runs commands on the machine executing Terraform. Required tools vary by resource and include package managers, system-management utilities, `git`, `ssh-keygen`, `crontab`, and macOS command-line tools. Protected operations may authenticate through `sudo`.

`git`, `ssh-keygen`, and AUR helpers are resolved when their operations run, so another resource can install them earlier in the same dependency-ordered apply. Pacman itself must already exist before Pacman or AUR objects are configured.

## Packages and Data Sources

Each `host_package_*` backend has a resource for lifecycle ownership and a data source for read-only lookup. AUR data sources use local Pacman state by default and query a verified AUR helper only when `include_remote = true`.

## Privileged Files

`host_system_file` and `host_sudoers_rule` resolve their privileged utility allowlist from protected system directories rather than the caller's `PATH`. Executables and their parent directories must be root-owned and not writable by group or other users.

## Runtime Metadata

User-scoped metadata, including file-block state and schedule scripts, defaults to `~/.local/state/terraform-provider-host` under `target_user`. A system-only provider has no user runtime directory.

## AUR Trust Boundary

`host_aur_helper` bootstraps `yay` or `paru` from the current AUR Git HEAD. AUR repositories and `PKGBUILD` files are user-contributed and mutable, and builds execute unsandboxed code as the Terraform user.

Review and trust every AUR package before applying it. Verifying that Pacman owns the helper executable does not audit the helper or package source.
