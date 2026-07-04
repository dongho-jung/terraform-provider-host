resource "host_dir" "projects" {
  path = "~/projects"
  mode = "0755"
}

resource "host_mac_dock_folder" "downloads" {
  path     = "~/Downloads"
  priority = 10
}

resource "host_mac_dock_folder" "projects" {
  path     = host_dir.projects.path
  priority = 20
}
