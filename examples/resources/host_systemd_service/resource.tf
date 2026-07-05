resource "host_package_pacman" "openssh" {
  name = "openssh"
}

resource "host_systemd_service" "sshd" {
  name    = "sshd.service"
  enabled = true
  running = true

  depends_on = [
    host_package_pacman.openssh,
  ]
}
