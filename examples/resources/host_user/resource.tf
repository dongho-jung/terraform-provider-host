data "host_group" "admin" {
  role = "admin"
}

resource "host_group" "developers" {
  name = "developers"
}

resource "host_user" "deploy" {
  username    = "deploy"
  full_name   = "Deploy User"
  shell       = "/bin/zsh"
  create_home = true

  groups = [
    data.host_group.admin.name,
    host_group.developers.name,
  ]
}
