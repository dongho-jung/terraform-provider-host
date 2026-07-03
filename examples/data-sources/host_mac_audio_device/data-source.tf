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
      name = "BlackHole 2ch"
    },
  ]
}
