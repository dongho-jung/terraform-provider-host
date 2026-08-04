---
page_title: "Host Provider: Runtime and Security"
description: |-
  Local command execution, runtime metadata, privileged operations, and the AUR trust boundary.
---

# Runtime and Security

## Local Execution

The provider runs commands on the machine executing Terraform. Required tools vary by resource and include package managers, system-management utilities, `git`, `ssh-keygen`, `crontab`, and macOS command-line tools. Protected operations may authenticate through `sudo`.

`git`, `ssh-keygen`, and AUR helpers are resolved when their operations run. When `aur_helper` is configured, AUR package mutations install missing `base-devel` and `git` prerequisites and bootstrap that helper. Planning, refresh, and data-source reads do not install bootstrap tooling. Pacman itself must already exist before Pacman or AUR objects are configured.

## Sudo Authentication

Privileged operations authenticate through `sudo` on the controlling terminal. The provider does this while it is being configured, before any resource is read or changed, and renews the timestamp in the background every 60 seconds for the rest of the run. Renewal matters because sudo's default `timestamp_timeout` is five minutes, which a large refresh or apply outlives.

Authenticating up front is not cosmetic. Terraform keeps streaming progress lines while a resource operation blocks, so a prompt raised partway through a run is interleaved with that output and easily missed. Only a prompt that precedes the run avoids it.

Refresh needs root too: reading a `host_sudoers_rule` or a root-only `host_system_file` requires root to detect drift, so `terraform plan` can ask for a password even though it changes nothing.

Set `sudo_preauth = false` for a user-scoped configuration that never needs root, so a run that would not otherwise touch `sudo` does not ask for a password:

```terraform
provider "host" {
  target_user  = "alice"
  sudo_preauth = false
}
```

Privileged operations then authenticate on demand instead, which is also the fallback whenever pre-authentication does not happen.

Pre-authentication is skipped, with a warning rather than an error, when the run is already root, when `sudo` is missing, or when no controlling terminal is available. A valid sudo timestamp is always reused, so a passwordless `sudo` configuration and a preceding `sudo -v` both prompt for nothing.

Each Terraform command runs its own provider process, so `terraform plan` and a later `terraform apply` authenticate separately unless the timestamp from the earlier command is still valid.

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
