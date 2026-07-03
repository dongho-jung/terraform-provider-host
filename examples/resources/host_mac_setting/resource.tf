resource "host_mac_setting" "dock_autohide" {
  domain = {
    apple = "dock"
  }

  key   = "autohide"
  value = true
}
