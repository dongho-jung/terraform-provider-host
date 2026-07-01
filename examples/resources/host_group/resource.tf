resource "host_group" "developers" {
  name = "developers"
}

data "host_group" "admin" {
  role = "admin"
}
