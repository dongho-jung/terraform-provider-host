resource "host_fstab_entry" "data" {
  name        = "data"
  device      = "UUID=11111111-2222-3333-4444-555555555555"
  mount_point = "/data"
  fs_type     = "ext4"
  options     = "defaults,noatime"
  pass        = 2
}
