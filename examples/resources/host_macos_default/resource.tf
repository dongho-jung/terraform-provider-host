resource "host_macos_default" "dock_autohide" {
  domain = "com.apple.dock"
  key    = "autohide"
  bool   = true

  restart = ["Dock"]
}

resource "host_macos_default" "dock_hide_recent_apps" {
  domain = "com.apple.dock"
  key    = "show-recents"
  bool   = false

  restart = ["Dock"]
}

resource "host_macos_default" "trackpad_tap_to_click" {
  domain = "com.apple.AppleMultitouchTrackpad"
  key    = "Clicking"
  bool   = true
}

resource "host_macos_default" "natural_scrolling" {
  domain = "NSGlobalDomain"
  key    = "com.apple.swipescrolldirection"
  bool   = true
}

resource "host_macos_default" "languages" {
  domain      = "NSGlobalDomain"
  key         = "AppleLanguages"
  string_list = ["ko-KR", "en-US"]
}

resource "host_macos_default" "clock_24h" {
  domain = "NSGlobalDomain"
  key    = "AppleICUForce24HourTime"
  bool   = true
}

resource "host_macos_default" "reduce_motion" {
  domain = "com.apple.universalaccess"
  key    = "reduceMotion"
  bool   = true
}

resource "host_macos_default" "show_battery_percent" {
  domain = "com.apple.menuextra.battery"
  key    = "ShowPercent"
  string = "YES"

  restart = ["SystemUIServer"]
}
