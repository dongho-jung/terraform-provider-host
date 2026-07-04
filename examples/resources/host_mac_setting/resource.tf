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
