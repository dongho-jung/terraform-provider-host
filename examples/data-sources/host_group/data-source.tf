data "host_group" "admin" {
  role = "admin"
}

resource "host_user" "deploy" {
  username = "deploy"
  groups   = [data.host_group.admin.name]
}
