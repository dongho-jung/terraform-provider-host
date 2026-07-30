## 0.15.0 (2026-07-30)

FEATURES:

- Add `host_package_dnf`, `host_package_pacman`, and `host_package_aur` data sources so every managed package backend also supports read-only installation and version lookup.

IMPROVEMENTS:

- Separate Host objects into system and user scopes. System-only provider configurations may omit `target_user`; user-scoped objects diagnose the missing user context, while `host_user` and `host_sudoers_rule` remain host-wide and may refer to users other than `target_user`.
- Require Dock, Login Item, and CoreAudio objects to run as provider `target_user` in that user's active macOS console session, with actionable login and targeted-apply diagnostics when the session is unavailable.
- Track desired and observed DNF and Homebrew `install_reason`, repair external reason drift to `user`/`on_request`, and skip DNF repository version queries when `ignore_version = true`.
- Preserve Terraform's refresh boundary for version-ignored DNF and Homebrew packages, keeping `terraform plan -refresh=false` based on prior state instead of performing live package lookups.
- Share DNF and Homebrew package snapshots across concurrent resources, cache macOS Dock, Login Item, and audio-device reads, compile the CoreAudio Swift helper once per source version, reduce systemd service status to one command, and reuse the planned Git remote commit during apply.
- Write only changed entries when updating grouped macOS settings, so unchanged defaults are not rewritten and their processes are not restarted.

FIXES:

- Hydrate whole-file content and symbolic-link source state during `host_file` and `host_link` imports so the first refresh can adopt the existing object instead of dropping it from state or failing on a missing required attribute.
- Atomically replace managed files and runtime metadata, including privileged fstab, sysctl, and systemd files, instead of truncating destinations in place.
- Serialize complete fstab and crontab read/modify/write transactions plus DNF, Pacman, AUR, and Homebrew package mutations across provider instances, and make macOS Dock refresh detect live deletion and ordering drift.
- Retry transient executable-busy errors when starting a newly compiled CoreAudio helper.
- Preserve command errors during systemd status reads and use a stable C locale when parsing human-readable package and host-management command output.

## 0.14.0 (2026-07-30)

BREAKING CHANGES:

- Remove automatic fallback to the working-directory `./.terraform-provider-host` runtime. Before upgrading a configuration that still relies on that directory, either set `runtime_dir` to its absolute path or copy its metadata to `~/.local/state/terraform-provider-host` and set that stable path explicitly. Previous schedule runtime directories are no longer migrated or removed automatically when `runtime_dir` changes.

FIXES:

- Respect Terraform's refresh boundary for version-ignored Pacman and AUR packages. In particular, `terraform plan -refresh=false` no longer bypasses state and reports computed-only `installed_version` changes.
- Update an existing `host_git_repo` remote URL or name in place instead of replacing the checkout. Updates first verify that the current remote still matches Terraform state and refuse to overwrite unexpected external drift.
- Preserve the released schema versions for `host_package_dnf`, `host_file_block`, `host_file`, and `host_link`, so states written by v0.13.0 remain readable by later provider builds.

## 0.13.0 (2026-07-20)

FEATURES:

- Add `host_aur_helper` to bootstrap and manage `yay` or `paru` directly with `git` and `makepkg`, including package variants such as `yay-bin`, without requiring an AUR helper to be present when the provider starts.
- Add `host_system_file` for atomic installation of privileged, root-owned regular files with a canonical group and a mode that is not writable by group or other users, source-backed content that stays out of Terraform state, explicit adoption, and guarded deletion.
- Add `host_sudoers_rule` for structured sudoers drop-ins. Rules are strictly validated with `visudo` and authorize only literal local users and exact, absolute commands invoked with no arguments.

IMPROVEMENTS:

- Share cached Pacman installed/explicit-package snapshots across concurrent resources, coalesce duplicate status queries, invalidate caches after mutations, and skip sync-database version queries when `ignore_version = true`.
- Add desired and observed `install_reason` state to Pacman packages, AUR packages, and AUR helpers. Managed packages converge to `explicit`, while refresh reports external drift to `dependency` so the next apply can repair it.
- Store runtime metadata for new configurations under `~/.local/state/terraform-provider-host` in the provider target user's home. Existing working-directory `.terraform-provider-host` runtimes remain the default when detected, allowing an explicit migration without silently abandoning stateful artifacts.
- Resolve `git`, `ssh-keygen`, and AUR helper executables when an operation needs them instead of only during provider configuration. Package resources can therefore install those tools earlier in the same dependency-ordered apply; AUR remote version lookup is deferred during planning until a verified helper is available.

FIXES:

- Detect missing, corrupt, or mismatched schedule scripts, metadata, and cron entries and produce an in-place repair on the next apply. Runtime files are replaced atomically; migration removes a previous schedule runtime only when it is under the provider-computed legacy root for the current working directory and its metadata verifies the same schedule, leaving explicit, unknown, or corrupt previous locations untouched.
- Serialize each target crontab read/modify/write transaction within the provider process so parallel `host_schedule` resources do not overwrite one another.

SECURITY:

- Resolve privileged system-file and sudoers utilities only from trusted system directories, requiring root ownership, non-writable executables, and protected parent directories instead of trusting the caller's `PATH`.
- Require every managed system-file destination parent to be an existing, root-owned real directory that is not writable by group or other users; refuse symlink destinations, source bytes changed after planning, and deletion when the checksum, root ownership, safe mode, or special-bit checks fail.
- Verify that a discovered AUR helper executable is owned by its configured Pacman package before adopting, using, or removing it. Helper bootstrap runs `makepkg` as the unprivileged Terraform user with controlled non-interactive Pacman authentication after sudo validation.
- Document that AUR repositories are user-contributed, mutable Git HEADs whose `PKGBUILD` files execute unsandboxed code, and require users to review and trust packages before applying them.

## 0.12.0 (2026-07-08)

FEATURES:

- Add `host_package_aur` for AUR package management through an AUR helper (`yay` or `paru`). The helper runs as the invoking user and shares the pacman database lock with `host_package_pacman`.
- Add `host_file_block` import support with `<path>:<block name>:<block id>` import IDs.

FIXES:

- Remove generated schedule runtime files when `host_schedule` creation fails before the resource is recorded in state, so failed applies no longer leave orphaned `schedules/<id>` directories behind.

## 0.11.2 (2026-07-07)

FIXES:

- Serialize mutating `host_package_pacman` operations behind a process-wide lock so parallel installs no longer fail with "unable to lock database" on the pacman `db.lck`. Read-only queries remain unserialized.

## 0.11.1 (2026-07-05)

FIXES:

- Preserve empty `host_user.groups` as an empty set during refresh.

## 0.11.0 (2026-07-05)

BREAKING CHANGES:

- Require provider `target_user`; the provider now manages one local user per configuration.
- Simplify `host_user` for target-user bootstrap with `name` and `home_dir` attributes.
- Remove the `data.host_group` role lookup; use explicit group names and `host_group` resource references.
- Remove `user` and `scope` from `host_schedule`; schedules now target the provider `target_user` crontab.

FEATURES:

- Allow provider `home_dir` to be set explicitly when bootstrapping a `target_user` that does not exist yet.
- Add `host_group` for local group bootstrap.
- Add `host_hostname` for system hostname management.
- Add `host_package_pacman` for Arch Linux package management.
- Add `host_fstab_entry` for provider-owned `/etc/fstab` entry blocks.
- Add `host_keymap` for Linux virtual console keymap management.
- Add `host_locale` for Linux system locale management.
- Add `host_sysctl` for Linux sysctl key management through `/etc/sysctl.d`.
- Add `host_systemd_service` for systemd service enabled/running state.
- Add `host_systemd_unit` for systemd unit file management with daemon reloads.
- Add `host_timezone` for system timezone management.

## 0.10.0 (2026-07-04)

BREAKING CHANGES:

- Remove `host_mac_dock`; manage Dock entries with `host_mac_dock_app` and `host_mac_dock_folder`.
- Remove macOS settings group selector special cases; `groups` now uses the supplied defaults domain as-is.

## 0.9.0 (2026-07-04)

FEATURES:

- Add `host_mac_dock_app` and `host_mac_dock_folder` for item-level Dock management with unique priorities.

## 0.8.0 (2026-07-04)

FEATURES:

- Add provider-level `target_user` for user home discovery and user-scoped resource defaults.

IMPROVEMENTS:

- Let `host_schedule` default to the provider `target_user` for user-scoped schedules.

## 0.7.0 (2026-07-04)

BREAKING CHANGES:

- Remove provider executable path override arguments such as `brew_path`, `git_path`, `swift_path`, and `osascript_path`; provider helpers are now resolved from `PATH`.

FEATURES:

- Add provider-level `home_dir` for leading `~` expansion in host paths.
- Add the `host_package_brew` data source for reading Homebrew package and cask app metadata.

FIXES:

- Resolve host paths and runtime metadata from the configured provider target user.
- Compare resolved paths when deciding replacement for path-based resources, reducing noisy diffs between equivalent `~` and absolute paths.

## 0.6.0 (2026-07-04)

BREAKING CHANGES:

- Simplify `host_mac_setting` and `host_mac_settings` to use exact macOS defaults domains only.
- Remove macOS settings domain aliases such as `dock`, `global`, `screenshot`, and `raw:<domain>`.

FEATURES:

- Add `app_path` and `app_paths` computed attributes to `host_package_brew` for Homebrew cask `.app` artifacts.

## 0.5.0 (2026-07-04)

FEATURES:

- Add `host_ssh_key` for local SSH keypair creation/adoption without storing private key material in state.
- Add `host_ssh_config_host` for Terraform-owned OpenSSH client `Host` blocks.
- Add `host_mac_login_item` for macOS app Login Items.

## 0.4.0 (2026-07-03)

FEATURES:

- Add provider-level runtime and executable path overrides.
- Add `host_file` and `host_schedule` import support.
- Add `ignore_version` to DNF and Homebrew package resources, defaulting to package presence management without upgrade diffs.
- Allow exact Homebrew package version checks when explicitly configured.

IMPROVEMENTS:

- Simplify the README and point users to Terraform Registry documentation.

## 0.3.0 (2026-07-03)

FEATURES:

- Simplify host provider APIs.
- Simplify macOS settings groups.

## 0.2.0 (2026-07-03)

FIXES:

- Avoid repeated Homebrew cask sudo prompts.

## 0.1.0 (2026-07-03)

FEATURES:

- Initial Terraform provider scaffold.
