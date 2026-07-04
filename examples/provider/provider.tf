terraform {
  required_providers {
    host = {
      source = "dongho-jung/host"
    }
  }
}

variable "target_user" {
  type    = string
  default = "alice"
}

provider "host" {
  target_user = var.target_user
}
