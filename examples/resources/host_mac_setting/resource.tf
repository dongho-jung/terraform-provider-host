resource "host_mac_setting" "screenshot_style" {
  domain = "com.apple.screencapture"
  key    = "style"
  value  = "selection"
}

resource "host_mac_setting" "screenshot_delay" {
  domain = "com.apple.screencapture"
  key    = "captureDelay"
  value  = 5
}

# A dictionary value is written as a property list. `merge` manages only the
# listed entries, so this disables one keyboard shortcut without replacing the
# rest of the shortcut table.
resource "host_mac_setting" "spotlight_shortcut" {
  domain = "com.apple.symbolichotkeys"
  key    = "AppleSymbolicHotKeys"
  merge  = true

  value = {
    "64" = {
      enabled = false
      value = {
        parameters = [65535, 49, 1048576]
        type       = "standard"
      }
    }
  }
}
