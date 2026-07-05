resource "host_package_pacman" "git" {
  name = "git"
}

resource "host_package_pacman" "neovim" {
  name = "neovim"
}

resource "host_package_pacman" "ripgrep" {
  name = "ripgrep"
}
