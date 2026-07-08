resource "host_package_aur" "wl_kbptr" {
  name = "wl-kbptr"
}

# ignore_version defaults to true: presence is managed without planning
# rebuilds every time the AUR publishes a new version.
resource "host_package_aur" "claude_code" {
  name = "claude-code"
}
