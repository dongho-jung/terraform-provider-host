resource "host_package_pacman" "base_devel" {
  name = "base-devel"
}

resource "host_package_pacman" "git" {
  name = "git"
}

resource "host_aur_helper" "yay" {
  name = "yay"

  depends_on = [
    host_package_pacman.base_devel,
    host_package_pacman.git,
  ]
}

resource "host_package_aur" "wl_kbptr" {
  name = "wl-kbptr"

  depends_on = [
    host_aur_helper.yay,
  ]
}

# ignore_version defaults to true: presence is managed without planning
# rebuilds every time the AUR publishes a new version.
resource "host_package_aur" "claude_code" {
  name = "claude-code"

  depends_on = [
    host_aur_helper.yay,
  ]
}
