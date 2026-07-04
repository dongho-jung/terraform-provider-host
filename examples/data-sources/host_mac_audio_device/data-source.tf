resource "host_package_brew" "blackhole_2ch" {
  name         = "blackhole-2ch"
  package_type = "cask"
}

data "host_mac_audio_device" "blackhole_2ch" {
  name = "BlackHole 2ch"

  depends_on = [
    host_package_brew.blackhole_2ch,
  ]
}

data "host_mac_audio_device" "headphones" {
  builtin_output = "headphones"
}

resource "host_mac_audio_multi_output" "default" {
  name = "Multi-Output Device"

  primary_device = {
    uid = data.host_mac_audio_device.headphones.uid
  }

  devices = [
    {
      uid = data.host_mac_audio_device.headphones.uid
    },
    {
      uid = data.host_mac_audio_device.blackhole_2ch.uid
    },
  ]

  sample_rate_hz = 48000
  default_output = false
  system_output  = false
}
