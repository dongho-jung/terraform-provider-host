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

Cask installs, upgrades, and removals may require macOS administrator authentication because Homebrew can call `sudo` internally. Plans that will mutate a cask emit a warning. During apply, the provider serializes Homebrew mutations, opens the current terminal for cask commands, prompts once with `Terraform provider host sudo password:` when sudo is not already authenticated, and keeps that sudo lease alive for later cask operations in the same Terraform run. If you want Terraform's UI to show only one Homebrew resource at a time, run `terraform apply -parallelism=1`.

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

Use `host_file` to manage a whole file without markers, or use `host_file` and `host_file_block` together to manage Terraform-owned sections inside an existing host file such as `~/.zshrc`.

Whole-file mode keeps the rendered file clean:

```hcl
resource "host_file" "zshrc" {
  path = "~/.zshrc"

  content = trimspace(<<-EOT
    export EDITOR=nvim
    eval "$(starship init zsh)"
  EOT
  )
}
```

Section mode can either preserve unmanaged file content with generated markers, or render a marker-free file while still letting separate resources contribute content.

The default `render = "markers"` mode preserves unmanaged file content and writes only content between its generated markers. Use `render = "clean"` when Terraform should render the whole file without markers while still accepting component-owned `host_file_block` resources. Clean mode stores block tracking metadata under `~/.terraform-provider-host/host_files`, so the rendered host file stays readable.

Terraform can only use `block["alias"]` addressing when `block` is a map, so declare file sections with map syntax:

```hcl
resource "host_file" "zshrc" {
  path   = "~/.zshrc"
  render = "clean"

  block = {
    options = {
      priority = 10
      content = trimspace(<<-EOT
        setopt autocd
        bindkey -v
      EOT
      )
    }
    alias = {
      priority = 20
    }
    functions = {
      priority = 30
    }
  }
}

resource "host_file_block" "foo_alias" {
  file_block = host_file.zshrc.block["alias"]
  content    = "alias foo=foobar"
}

resource "host_file_block" "bar_alias" {
  file_block = host_file.zshrc.block["alias"]
  content    = "alias bar=barbaz"
}

resource "host_file_block" "foo_function" {
  file_block = host_file.zshrc.block["functions"]
  content    = "foo() { echo foo }"
}

resource "host_file_block" "bar_function" {
  file_block = host_file.zshrc.block["functions"]
  content    = "bar() { echo bar }"
}
```

Use `block.<name>.content` for content that belongs to the host file itself, such as shell options, key bindings, or helper functions. `host_file_block` resources targeting the same section are rendered after that inline content.

Multiple `host_file_block` resources in the same file section are ordered by `priority`, then `content`. The default priority is `0`; set a lower or higher priority only when lexical content order is not enough.

Sections are ordered by `block.<name>.priority`, then section name. The default section priority is `0`.

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
