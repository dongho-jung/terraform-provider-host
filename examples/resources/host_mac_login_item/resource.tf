resource "host_package_brew" "hammerspoon" {
  name         = "hammerspoon"
  package_type = "cask"
}

resource "host_mac_login_item" "hammerspoon" {
  path = host_package_brew.hammerspoon.app_path
}
