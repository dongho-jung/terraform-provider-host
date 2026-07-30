---
page_title: "Host Provider: Scopes and User Contexts"
description: |-
  System and user scope classification, multi-user operation, bootstrap, and macOS desktop-session requirements.
---

# Scopes and User Contexts

Host objects use two ownership scopes:

- **System scope** manages host-wide state and does not require provider `target_user`.
- **User scope** manages one local user's state and requires provider `target_user`.

A provider configured with `target_user` can manage both scopes. Use `provider "host" {}` only for a system-only configuration.

## Scope Reference

| Scope | Resources | Data sources |
|---|---|---|
| System | `host_package_dnf`, `host_package_pacman`, `host_hostname`, `host_timezone`, `host_locale`, `host_keymap`, `host_sysctl`, `host_systemd_unit`, `host_systemd_service`, `host_fstab_entry`, `host_system_file`, `host_sudoers_rule`, `host_group`, `host_user` | `host_package_dnf`, `host_package_pacman` |
| User | `host_aur_helper`, `host_package_aur`, `host_package_brew`, `host_dir`, `host_file`, `host_file_block`, `host_link`, `host_git_repo`, `host_ssh_key`, `host_ssh_config_host`, `host_schedule`, all `host_mac_*` resources | `host_package_aur`, `host_package_brew`, `host_mac_audio_device` |

AUR objects are user-scoped because their helper and package builds run as the invoking user, even though installation ultimately changes Pacman state. `host_user` and `host_sudoers_rule` are system-scoped and may refer to users other than `target_user`.

## User Context

User-scoped paths expand `~` against the configured user's home. `home_dir` is discovered from `target_user` unless explicitly set, and `runtime_dir` defaults to `~/.local/state/terraform-provider-host`.

`target_user` selects paths and user-owned backends; it does not generally impersonate that account. Run user-scoped configurations as `target_user`, not through root or another user. `host_schedule` is the exception and can use privileged `crontab -u`.

A system-only provider has no home context. For example, `host_system_file.source` must use an absolute or relative path instead of `~`.

## Multiple Users

One provider instance carries one user context. Use separate Terraform root modules or workspaces for independently applied users. Provider aliases are also possible, but each user's objects must be applied while running as that user.

## Bootstrapping a User

Provider configuration is evaluated before resources. To create a future `target_user` with `host_user`, either:

1. Start with a system-only provider, apply `host_user`, then configure `target_user`.
2. Configure `target_user` with an explicit `home_dir`, apply `host_user` first with `-target`, then run a normal apply.

## macOS Desktop Sessions

Dock items, Login Items, CoreAudio device data sources, and CoreAudio multi-output devices additionally require Terraform to run as `target_user` while that user owns the active macOS console session. Persistent `host_mac_setting` and `host_mac_settings` preferences do not require an active desktop session.

If a session-bound object belongs to another or inactive user, log in to the macOS desktop as that user and run Terraform from that session without `sudo`. To stage specific unaffected objects first:

```shell
terraform apply -target=<resource-address>
```

Repeat `-target` for additional addresses, then run a normal apply from the correct user session.
