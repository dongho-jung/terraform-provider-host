resource "host_macos_default" "dock_autohide" {
  domain = "com.apple.dock"
  key    = "autohide"
  bool   = true
}
