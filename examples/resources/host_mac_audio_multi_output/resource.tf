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

resource "host_mac_audio_multi_output" "default" {
  name = "Multi-Output Device"

  primary_device = {
    uid = data.host_mac_audio_device.blackhole_2ch.uid
  }

  devices = [
    {
      builtin_output = "headphones"
    },
    {
      uid = data.host_mac_audio_device.blackhole_2ch.uid
    },
  ]

  sample_rate_hz = 48000
  default_output = false
  system_output  = false

  depends_on = [
    host_package_brew.blackhole_2ch,
  ]
}
