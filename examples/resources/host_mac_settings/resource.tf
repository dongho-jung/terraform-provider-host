resource "host_mac_settings" "settings" {
  groups = {
    dock = {
      domain = {
        apple = "dock"
      }

      settings = {
        autohide = {
          key   = "autohide"
          value = true
        }

        hide_recent_apps = {
          key   = "show-recents"
          value = false
        }
      }
    }

    global = {
      domain = {
        global = true
      }

      settings = {
        languages = {
          key   = "AppleLanguages"
          value = ["ko-KR", "en-US"]
        }
      }
    }

    battery = {
      domain = {
        apple = "menuextra.battery"
      }

      restart = ["SystemUIServer"]

      settings = {
        show_percent = {
          key   = "ShowPercent"
          value = "YES"
        }
      }
    }
  }
}
