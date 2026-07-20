resource "host_system_file" "vpn_up" {
  # Source-backed bytes are hashed but are not copied into Terraform state.
  source      = "${path.module}/vpn-up"
  destination = "/usr/local/bin/vpn-up"

  mode              = "0755"
  delete_on_destroy = true
}
