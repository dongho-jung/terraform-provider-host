resource "host_schedule" "hourly_example" {
  every   = "1h"
  command = "date >> ~/tmp/host-schedule-example.log"
}

resource "host_schedule" "daily_example" {
  schedule = "0 3 * * *"
  command  = "echo daily >> ~/tmp/host-schedule-example.log"

  stdout_path = "~/tmp/host-schedule-example.out.log"
  stderr_path = "~/tmp/host-schedule-example.err.log"
}
