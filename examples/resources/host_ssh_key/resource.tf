# ssh-keygen is resolved when this resource needs it, so a package resource may
# install OpenSSH earlier in the same dependency-ordered apply.
resource "host_ssh_key" "github" {
  path              = "~/.ssh/id_ed25519"
  comment           = "alice@example.com"
  delete_on_destroy = false
}
