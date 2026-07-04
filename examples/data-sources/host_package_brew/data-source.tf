data "host_package_brew" "google_chrome" {
  name         = "google-chrome"
  package_type = "cask"
}

resource "host_mac_login_item" "google_chrome" {
  path = data.host_package_brew.google_chrome.app_path
}
