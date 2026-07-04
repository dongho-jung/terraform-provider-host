data "host_group" "admin" {
  role = "admin"
}

resource "host_user" "workstation" {
  username = "workstation"
  groups   = [data.host_group.admin.name]
}
