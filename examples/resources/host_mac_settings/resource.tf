resource "host_mac_settings" "settings" {
  groups = {
    "com.apple.dock" = {
      autohide       = true
      "show-recents" = false
    }

    NSGlobalDomain = {
      AppleLanguages = ["ko-KR", "en-US"]
    }

    "com.apple.menuextra.battery" = {
      ShowPercent = "YES"
    }
  }
}
