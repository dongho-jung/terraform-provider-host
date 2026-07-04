resource "host_package_dnf" "git" {
  name    = "git"
  version = "latest"
}

resource "host_package_dnf" "neovim" {
  name = "neovim"
}

resource "host_package_dnf" "ripgrep" {
  name = "ripgrep"
}
