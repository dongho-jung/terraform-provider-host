resource "host_dir" "projects" {
  path = "~/projects"
  mode = "0755"
}

resource "host_dir" "zsh" {
  path = "~/.zsh"
  mode = "0755"
}

resource "host_dir" "neovim_data" {
  path = "~/.local/share/nvim"
  mode = "0755"
}
