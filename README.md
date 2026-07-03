# Terraform Provider Host

Terraform provider for managing local host configuration with Terraform.

## Documentation

User documentation is published on the Terraform Registry:

**https://registry.terraform.io/providers/dongho-jung/host/latest/docs**

Resource and data source documentation, including examples and schemas, lives under that Registry docs page.

Generated documentation is also checked into this repository under `docs/`. Regenerate it after schema or example changes:

```shell
make generate
```

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
