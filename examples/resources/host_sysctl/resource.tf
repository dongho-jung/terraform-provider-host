resource "host_sysctl" "ip_forward" {
  key   = "net.ipv4.ip_forward"
  value = "1"
}
