data "host_group" "admin" {
  role = "admin"
}

resource "host_group" "developers" {
  name = "developers"
}

resource "host_user" "workstation" {
  username    = "workstation"
  full_name   = "Workstation User"
  shell       = "/bin/zsh"
  create_home = true

  groups = [
    data.host_group.admin.name,
    host_group.developers.name,
  ]

  lifecycle {
    prevent_destroy = true
  }
}
