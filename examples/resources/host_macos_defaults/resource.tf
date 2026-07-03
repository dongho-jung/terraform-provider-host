resource "host_macos_defaults" "settings" {
  defaults = {
    dock_autohide = {
      domain = "com.apple.dock"
      key    = "autohide"
      bool   = true
    }

    dock_hide_recent_apps = {
      domain = "com.apple.dock"
      key    = "show-recents"
      bool   = false
    }

    trackpad_tap_to_click = {
      domain = "com.apple.AppleMultitouchTrackpad"
      key    = "Clicking"
      bool   = true
    }

    languages = {
      domain      = "NSGlobalDomain"
      key         = "AppleLanguages"
      string_list = ["ko-KR", "en-US"]
    }

    show_battery_percent = {
      domain  = "com.apple.menuextra.battery"
      key     = "ShowPercent"
      string  = "YES"
      restart = ["SystemUIServer"]
    }
  }
}
