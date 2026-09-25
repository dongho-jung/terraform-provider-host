resource "host_mac_permission" "hammerspoon_accessibility" {
  service = "accessibility"
  client  = "org.hammerspoon.Hammerspoon"
}

resource "host_mac_permission" "karabiner_input_monitoring" {
  service = "input_monitoring"
  client  = "org.pqrs.Karabiner-Elements.Settings"

  # Report a missing grant instead of failing the apply, and open the pane that
  # grants it.
  required                   = false
  open_settings_when_missing = true
}
