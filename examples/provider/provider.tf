terraform {
  required_providers {
    host = {
      source = "dongho-jung/host"
    }
  }
}

provider "host" {
  target_user                  = "dongho"
  aur_helper                   = "yay"
  aur_remove_make_dependencies = true
  aur_clean_after              = true

  # runtime_dir defaults to ~/.local/state/terraform-provider-host for this user.
}
