resource "host_dir" "projects" {
  path = "~/projects"
  mode = "0755"
}

resource "host_dir" "zsh" {
  path = "~/.zsh"
  mode = "0755"
}

# Git is resolved when this resource is planned or applied. On a fresh Arch
# host, add an explicit dependency on the host_package_pacman resource that
# installs git earlier in the same apply.
resource "host_git_repo" "alias_tips" {
  url  = "https://github.com/djui/alias-tips.git"
  path = "${host_dir.zsh.path}/alias-tips"

  ref          = "master"
  track_remote = true

  depends_on = [
    host_dir.zsh,
  ]
}

resource "host_git_repo" "dotfiles" {
  url  = "git@github.com:example/dotfiles.git"
  path = "${host_dir.projects.path}/dotfiles"

  delete_on_destroy = false
}
