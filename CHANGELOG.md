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

- Resolve host paths and runtime metadata per provider instance so provider aliases can target different users safely.
- Compare resolved paths when deciding replacement for path-based resources, reducing noisy diffs between equivalent `~` and absolute paths.

## 0.6.0 (2026-07-04)

BREAKING CHANGES:

- Simplify `host_mac_setting` and `host_mac_settings` to use raw macOS defaults domains only.
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
