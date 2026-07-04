resource "host_ssh_key" "github" {
  path    = "~/.ssh/id_ed25519_github"
  type    = "ed25519"
  comment = "github"
}
