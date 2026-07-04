resource "host_package_brew" "terraform" {
  name = "terraform"
  tap  = "hashicorp/tap"
}

resource "host_package_brew" "terraform_ls" {
  name = "terraform-ls"
}

resource "host_package_brew" "neovim" {
  name = "neovim"
}

resource "host_package_brew" "starship" {
  name = "starship"
}

resource "host_package_brew" "eza" {
  name = "eza"
}

resource "host_package_brew" "google_chrome" {
  name         = "google-chrome"
  package_type = "cask"
}

resource "host_package_brew" "hammerspoon" {
  name         = "hammerspoon"
  package_type = "cask"
}

resource "host_package_brew" "font_inconsolata_nerd_font" {
  name         = "font-inconsolata-nerd-font"
  package_type = "cask"
}
