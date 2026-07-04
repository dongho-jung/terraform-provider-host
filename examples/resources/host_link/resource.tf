resource "host_package_brew" "neovim" {
  name = "neovim"
}

resource "host_link" "neovim_config" {
  source      = "${path.module}/config/nvim"
  destination = "~/.config/nvim"

  depends_on = [
    host_package_brew.neovim,
  ]
}

resource "host_link" "hammerspoon_config" {
  source      = "${path.module}/config/hammerspoon"
  destination = "~/.hammerspoon"
}
