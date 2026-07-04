resource "host_ssh_key" "github" {
  path              = "~/.ssh/id_ed25519"
  comment           = "alice@example.com"
  delete_on_destroy = false
}

resource "host_ssh_config_host" "github" {
  host            = "github.com"
  hostname        = "github.com"
  user            = "git"
  identity_file   = host_ssh_key.github.path
  identities_only = true
  adopt_existing  = true

  extra_options = {
    AddKeysToAgent = "yes"
    UseKeychain    = "yes"
  }
}
