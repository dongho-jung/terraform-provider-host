resource "host_system_file" "vpn_up" {
  source      = "${path.module}/vpn-up"
  destination = "/usr/local/bin/vpn-up"

  mode              = "0755"
  delete_on_destroy = true
}

resource "host_sudoers_rule" "vpn" {
  name = "vpn"
  user = "dongho"

  commands = [
    host_system_file.vpn_up.destination,
  ]

  run_as   = "root"
  nopasswd = true
}
