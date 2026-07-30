data "host_package_pacman" "git" {
  name = "git"
}

output "pacman_git_install_reason" {
  value = data.host_package_pacman.git.install_reason
}
