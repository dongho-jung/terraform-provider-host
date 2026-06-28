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

Use `host_dnf_package` to manage one DNF package per Terraform resource. Creating a resource installs the package if needed and marks its DNF install reason as `User`, so DNF does not treat it as an automatically installed dependency.

Do not use DNF reason `User` as a perfect inventory of packages a human intentionally installed. Fedora installers and image defaults can also mark packages with reason `User`.

The default version policy is `latest`. During planning the provider reads the installed package EVR and the latest candidate EVR from enabled DNF repositories. If they differ, Terraform plans an in-place update and apply runs `dnf upgrade`.

Exact version pinning is not implemented yet; `version = "latest"` is currently the only supported policy.

Removing a resource runs `dnf remove` for that package.

```hcl
resource "host_dnf_package" "git" {
  name    = "git"
  version = "latest"
}
```

Mutating DNF operations require root privileges. Plans that will install, upgrade, remove, or mark a package emit a sudo warning. When Terraform is not running as root, the provider prints the DNF command that needs elevation, runs `sudo -v`, then executes the command through sudo. If Terraform cannot prompt for a sudo password in your terminal, run `sudo -v` before `terraform apply` or configure passwordless sudo for local package management.

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
