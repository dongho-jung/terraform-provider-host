data "host_package_dnf" "git" {
  name = "git"
}

output "dnf_git_installed_version" {
  value = data.host_package_dnf.git.installed_version
}
