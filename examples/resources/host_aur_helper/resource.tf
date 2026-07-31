# Review and trust yay's mutable AUR PKGBUILD before allowing Terraform to
# build it as the target user.
resource "host_aur_helper" "yay" {
  name = "yay"
}

resource "host_package_aur" "wl_kbptr" {
  name = "wl-kbptr"

  depends_on = [
    host_aur_helper.yay,
  ]
}
