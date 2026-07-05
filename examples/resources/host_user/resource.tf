provider "host" {
  target_user = "dongho"
  home_dir    = "/Users/dongho"
}

resource "host_group" "developers" {
  name = "developers"
}

resource "host_user" "target" {
  name      = "dongho"
  full_name = "Dongho Jung"
  home_dir  = "/Users/dongho"
  shell     = "/bin/zsh"

  groups = [
    "admin",
    host_group.developers.name,
  ]

  lifecycle {
    prevent_destroy = true
  }
}
