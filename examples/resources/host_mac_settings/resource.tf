resource "host_mac_settings" "settings" {
  groups = {
    dock = {
      autohide       = true
      "show-recents" = false
    }

    global = {
      AppleLanguages = ["ko-KR", "en-US"]
    }

    "menuextra.battery" = {
      ShowPercent = "YES"
    }
  }
}
