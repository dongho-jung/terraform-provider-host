resource "host_package_brew" "hammerspoon" {
  name         = "hammerspoon"
  package_type = "cask"
}

resource "host_package_brew" "itsycal" {
  name         = "itsycal"
  package_type = "cask"
}

resource "host_package_brew" "shottr" {
  name         = "shottr"
  package_type = "cask"
}

resource "host_mac_login_item" "hammerspoon" {
  path = host_package_brew.hammerspoon.app_path
}

resource "host_mac_login_item" "itsycal" {
  path = host_package_brew.itsycal.app_path
}

resource "host_mac_login_item" "shottr" {
  path = host_package_brew.shottr.app_path
}
