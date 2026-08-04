terraform {
  required_providers {
    host = {
      source = "dongho-jung/host"
    }
  }
}

# target_user defaults to the user running Terraform, so an empty block manages
# both system-scoped and user-scoped objects for that user. Name it explicitly
# to manage a different user's state.
provider "host" {}
