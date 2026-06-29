resource "host_package_brew" "bat" {
  name = "bat"
}

resource "host_package_brew" "firefox" {
  name         = "firefox"
  package_type = "cask"
}

resource "host_package_brew" "terraform" {
  name = "terraform"
  tap  = "hashicorp/tap"
}
