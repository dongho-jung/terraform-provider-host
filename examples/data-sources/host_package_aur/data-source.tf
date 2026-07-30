data "host_package_aur" "wl_kbptr" {
  name = "wl-kbptr"

  # Local installation metadata does not require an AUR helper.
  # Set this to true to query the candidate version through yay or paru.
  include_remote = false
}

output "aur_wl_kbptr_installed_version" {
  value = data.host_package_aur.wl_kbptr.installed_version
}
