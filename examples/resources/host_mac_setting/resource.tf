resource "host_mac_setting" "dock_autohide" {
  domain = "com.apple.dock"
  key    = "autohide"
  value  = true
}
