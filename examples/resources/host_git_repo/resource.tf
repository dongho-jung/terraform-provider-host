resource "host_git_repo" "alias_tips" {
  url  = "https://github.com/djui/alias-tips.git"
  path = "~/.zsh/alias-tips"

  ref          = "master"
  track_remote = true
}
