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

## Schedules

Use `host_schedule` to manage a user schedule without configuring a separate schedule name. The provider generates an internal ID, writes runtime files under `./.terraform-provider-host/schedules/<id>`, and installs a managed entry in the current user's crontab.

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
```

The `schedule` attribute supports numeric cron fields, lists, ranges, steps, and `@hourly`, `@daily`, `@weekly`, `@monthly`, and `@yearly`. The rendered crontab entry points at the generated `run.sh`, so `crontab -l` shows the active schedules directly. On Fedora-like systems where `crontab` is missing, the provider tries to install `cronie` through DNF before writing the schedule.

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
