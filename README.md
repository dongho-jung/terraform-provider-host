# Terraform Provider Host

Terraform provider scaffold for `registry.terraform.io/dongho-jung/host`, built with the Terraform Plugin Framework.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- [Go](https://go.dev/doc/install) >= 1.25

## Building

```shell
go install
```

## Testing

```shell
make test
make testacc
```

Acceptance tests use `TF_ACC=1` and should be kept focused on real provider behavior as resources and data sources are added.

## Documentation

Provider documentation is generated from the framework schema and examples:

```shell
make generate
```

Generated files live under `docs/`. Handwritten Terraform examples live under `examples/`.

## DNF Package Management

Use `host_package_dnf` to manage one DNF package per Terraform resource. Creating a resource installs the package if needed and marks its DNF install reason as `User`, so DNF does not treat it as an automatically installed dependency.

Do not use DNF reason `User` as a perfect inventory of packages a human intentionally installed. Fedora installers and image defaults can also mark packages with reason `User`.

The default version policy is `latest`. During planning the provider reads the installed package EVR and the latest candidate EVR from enabled DNF repositories. If they differ, Terraform plans an in-place update and apply runs `dnf upgrade`.

Exact version pinning is not implemented yet; `version = "latest"` is currently the only supported policy.

Removing a resource runs `dnf remove` for that package.

```hcl
resource "host_package_dnf" "git" {
  name    = "git"
  version = "latest"
}
```

Mutating DNF operations require root privileges. Plans that will install, upgrade, remove, or mark a package emit a sudo warning. When Terraform is not running as root, the provider prints the DNF command that needs elevation, runs `sudo -v`, then executes the command through sudo. If Terraform cannot prompt for a sudo password in your terminal, run `sudo -v` before `terraform apply` or configure passwordless sudo for local package management.

## Homebrew Package Management

Use `host_package_brew` to manage one Homebrew formula or cask per Terraform resource. The default package type is `formula`; set `package_type = "cask"` for Homebrew casks.

The default version policy is `latest`. During planning the provider reads Homebrew's JSON package metadata and plans an in-place update when the installed version is outdated. Cask upgrades use `brew upgrade --cask --greedy` so casks marked `auto_updates true` can still be reconciled by Terraform.

Removing a formula resource runs `brew uninstall --formula` and then `brew autoremove` by default. Removing a cask resource runs `brew uninstall --cask`; set `zap = true` to pass `--zap` on destroy.

Cask installs, upgrades, and removals may require macOS administrator authentication because Homebrew can call `sudo` internally. Plans that will mutate a cask emit a warning. During apply, the provider prompts once through the current terminal when sudo is not already authenticated, keeps that sudo lease alive for later cask operations in the same Terraform run, and prints reminders while it waits so the password prompt does not stay buried under Terraform status lines. If you miss the prompt, type your password in the same terminal and press Enter, or run `sudo -v` before `terraform apply`.

```hcl
resource "host_package_brew" "bat" {
  name = "bat"
}

resource "host_package_brew" "firefox" {
  name         = "firefox"
  package_type = "cask"
}

resource "host_package_brew" "terraform" {
  name = "terraform"
  tap  = "hashicorp/tap"
}
```

Set `tap` for packages that live outside Homebrew's default taps. The provider checks `brew tap`, runs `brew tap <tap>` during apply when needed, and then manages the package as `<tap>/<name>`.

## Host Directories

Use `host_dir` to create and permission a host directory:

```hcl
resource "host_dir" "projects" {
  path = "~/projects"
  mode = "0755"
}
```

Destroy removes an empty directory by default. Set `recursive_delete = true` only when Terraform should remove the entire directory tree.

## Host Users and Groups

Use `host_group` and `host_user` to manage local users and supplementary group membership. Passwords are intentionally not managed by this provider; use the operating system, MDM, or a secrets workflow for password setup.

Prefer references for group membership instead of repeating literal group names:

```hcl
data "host_group" "admin" {
  role = "admin"
}

resource "host_group" "developers" {
  name = "developers"
}

resource "host_user" "deploy" {
  username    = "deploy"
  full_name   = "Deploy User"
  shell       = "/bin/zsh"
  create_home = true

  groups = [
    data.host_group.admin.name,
    host_group.developers.name,
  ]
}
```

The `admin` role resolves to `admin` on macOS and the first existing `wheel` or `sudo` group on Linux. `host_user.groups` manages only the groups listed in Terraform state; other groups attached outside Terraform are left untouched. Removing a user does not remove the home directory unless `remove_home_on_destroy = true` is set.

Mutating user and group operations require root privileges. When Terraform is not running as root, the provider prompts through `sudo -v` before running commands such as `useradd`, `usermod`, `groupadd`, `dscl`, or `dseditgroup`.

## Host File Blocks

Use `host_file` to manage a whole file without markers, or use `host_file` and `host_file_block` together to manage Terraform-owned blocks inside an existing host file such as `~/.zshrc`.

Whole-file mode keeps the rendered file clean:

```hcl
resource "host_file" "zshrc" {
  path = "~/.zshrc"

  content = <<-EOT
    export EDITOR=nvim
    eval "$(starship init zsh)"
  EOT
}
```

Block mode renders a marker-free file while still letting separate resources contribute content. The provider stores block tracking metadata under `./.terraform-provider-host/host_files` relative to the Terraform working directory, so the rendered host file stays readable and Terraform can still reconcile component-owned snippets.

Declare file blocks with nested `block` blocks. Separate `host_file_block` resources target them through the computed `blocks` references:

```hcl
resource "host_file" "zshrc" {
  path = "~/.zshrc"

  block {
    name = "options"
    content = <<-EOT
      setopt autocd
      bindkey -v
    EOT
  }

  block {
    name = "alias"
  }

  block {
    name = "functions"
  }
}

resource "host_file_block" "foo_alias" {
  block   = host_file.zshrc.blocks.alias
  content = "alias foo=foobar"
}

resource "host_file_block" "bar_alias" {
  block   = host_file.zshrc.blocks.alias
  content = "alias bar=barbaz"
}

resource "host_file_block" "foo_function" {
  block = host_file.zshrc.blocks.functions
  content = <<-EOT
    foo() { echo foo }
  EOT
}

resource "host_file_block" "bar_function" {
  block = host_file.zshrc.blocks.functions
  content = <<-EOT
    bar() { echo bar }
  EOT
}
```

Use `block.content` for content that belongs to the host file itself, such as shell options, key bindings, or helper functions. `host_file_block` resources targeting the same file block are rendered after that inline content.

Multiple `host_file_block` resources in the same file block are ordered by `after` and `before` constraints first, then `content`. Prefer splitting a host file into more explicit file blocks when order is structurally important.

File blocks are ordered by declaration order. Use `block.after` or `block.before` when one declared block must move relative to another.

Terraform does not support single-quoted strings. Use heredocs when shell content contains quotes, command substitutions, or functions; the provider trims surrounding whitespace before rendering.

## Host Links

Use `host_link` to manage a symbolic link from a host destination path to a source file or directory in your Terraform working directory. Relative `source` paths are resolved from the Terraform working directory, so larger config trees can stay as normal files with editor and language-server support:

```hcl
resource "host_link" "nvim" {
  source      = "./nvim"
  destination = "~/.config/nvim"
}
```

The provider creates a symlink only. It refuses to replace an existing regular file or directory at `destination`; move existing content aside before applying.

## Git Repositories

Use `host_git_repo` to clone a Git repository into a host path. The `url` can be any remote accepted by `git clone`, including GitHub, GitLab, sourcehut, SSH URLs, HTTPS URLs, or a local path:

```hcl
resource "host_git_repo" "alias_tips" {
  url  = "https://github.com/djui/alias-tips.git"
  path = "~/.zsh/alias-tips"

  ref          = "master"
  track_remote = true
}
```

When `track_remote = true`, the provider resolves the latest remote commit for `ref` during planning and moves the checkout to that commit during apply. When false, the provider clones the repository and leaves an existing checkout at its current commit unless configuration changes. The checkout is detached when Terraform moves it to a specific commit.

## macOS Defaults

Use `host_macos_default` to manage one typed macOS `defaults` key. This is the low-level building block for Dock, menu bar, trackpad, language, clock, and accessibility settings:

```hcl
resource "host_macos_default" "dock_autohide" {
  domain = "com.apple.dock"
  key    = "autohide"
  bool   = true

  restart = ["Dock"]
}
```

For larger preference sets, use `host_macos_defaults` and define the keys in one map:

```hcl
resource "host_macos_defaults" "settings" {
  defaults = {
    dock_autohide = {
      domain = "com.apple.dock"
      key    = "autohide"
      bool   = true
    }

    languages = {
      domain      = "NSGlobalDomain"
      key         = "AppleLanguages"
      string_list = ["ko-KR", "en-US"]
    }

    show_battery_percent = {
      domain  = "com.apple.menuextra.battery"
      key     = "ShowPercent"
      string  = "YES"
      restart = ["SystemUIServer"]
    }
  }
}
```

Exactly one of `bool`, `int`, `float`, `string`, or `string_list` must be set. The provider uses the macOS `defaults` command when available and can restart affected processes with `restart`, such as `Dock`, `Finder`, or `SystemUIServer`.

When `restart` is omitted, the provider applies built-in restarts for known domains such as Dock, Finder, SystemUIServer menu extras, global preferences, trackpad preferences, and accessibility preferences. Set `restart = []` to disable restarts for a specific default.

Removing an entry from `host_macos_defaults.defaults` leaves that macOS setting in place unless the entry had `delete_on_destroy = true`.

Import existing values with `terraform import` using `domain:key`, `user:domain:key`, or `currentHost:domain:key`:

```shell
terraform import host_macos_default.dock_autohide user:com.apple.dock:autohide
```

## macOS Dock

Use `host_macos_dock` to manage Dock persistent apps and folders as whole ordered lists:

```hcl
resource "host_macos_dock" "default" {
  apps = [
    "/System/Applications/System Settings.app",
    "/Applications/Google Chrome.app",
  ]

  folders = [
    "/Users/dongho/Downloads",
  ]
}
```

The resource owns the full `persistent-apps` and `persistent-others` arrays. It preserves other Dock settings, restarts Dock by default after writes, and does not clear the Dock on destroy unless `delete_on_destroy = true`.

## Schedules

Use `host_schedule` to manage a user schedule without configuring a separate schedule name. The provider generates an internal ID, writes runtime files under `./.terraform-provider-host/schedules/<id>`, and installs a managed entry in a crontab.

Set either `every` for interval schedules or `schedule` for five-field cron-style calendar schedules:

```hcl
resource "host_schedule" "hourly_example" {
  every   = "1h"
  command = "date >> ~/tmp/host-schedule-example.log"
}

resource "host_schedule" "daily_example" {
  schedule = "0 3 * * *"
  command  = "echo daily >> ~/tmp/host-schedule-example.log"

  stdout_path = "~/tmp/host-schedule-example.out.log"
  stderr_path = "~/tmp/host-schedule-example.err.log"
}

resource "host_schedule" "system_example" {
  scope = "system"

  schedule = "0 4 * * *"
  command  = "/usr/local/bin/system-maintenance"
}
```

The default `scope = "user"` manages the current Terraform user's crontab. Set `user` to manage another user's crontab, or set `scope = "system"` to manage root's crontab. Non-current-user schedules use `crontab -u <user>` and prompt through sudo when Terraform is not already running as root.

The `schedule` attribute supports numeric cron fields, lists, ranges, steps, and `@hourly`, `@daily`, `@weekly`, `@monthly`, and `@yearly`. The rendered crontab entry points at the generated `run.sh`, so `crontab -l` or `sudo crontab -u <user> -l` shows the active schedules directly. On Fedora-like systems where `crontab` is missing, the provider tries to install `cronie` through DNF before writing the schedule.

## Local Development

Use a Terraform CLI development override while iterating locally:

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/dongho-jung/host" = "/path/to/go/bin"
  }

  direct {}
}
```

Then build the provider with `go install` and run Terraform from a separate working directory.
