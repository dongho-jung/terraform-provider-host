terraform {
  required_providers {
    host = {
      source = "dongho-jung/host"
    }
  }
}

provider "host" {
  target_user = "dongho"
  aur_helper  = "yay"

  # runtime_dir defaults to ~/.local/state/terraform-provider-host for this user.
}
