## 0.12.0 (2026-07-08)

FEATURES:

- Add `host_package_aur` for AUR package management through an AUR helper (`yay` or `paru`). The helper runs as the invoking user and shares the pacman database lock with `host_package_pacman`.
- Add `host_file_block` import support with `<path>:<block name>:<block id>` import IDs.

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
