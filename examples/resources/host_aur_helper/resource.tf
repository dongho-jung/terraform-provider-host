resource "host_package_pacman" "base_devel" {
  name = "base-devel"
}

resource "host_package_pacman" "git" {
  name = "git"
}

# Review and trust yay's mutable AUR PKGBUILD before allowing Terraform to
# build it as the target user.
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
