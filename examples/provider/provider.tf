terraform {
  required_providers {
    host = {
      source = "dongho-jung/host"
    }
  }
}

provider "host" {
  target_user = "dongho"

  # runtime_dir defaults to ~/.local/state/terraform-provider-host for this
  # target user unless a legacy ./.terraform-provider-host directory exists.
}
