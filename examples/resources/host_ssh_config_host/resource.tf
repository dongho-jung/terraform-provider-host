resource "host_ssh_key" "github" {
  path    = "~/.ssh/id_ed25519_github"
  type    = "ed25519"
  comment = "github"
}

resource "host_ssh_config_host" "github" {
  host            = "github.com"
  hostname        = "github.com"
  user            = "git"
  identity_file   = host_ssh_key.github.path
  identities_only = true
}
