resource "host_systemd_unit" "backup_service" {
  name = "restic-backup.service"

  content = <<-UNIT
    [Unit]
    Description=Run Restic backup

    [Service]
    Type=oneshot
    ExecStart=/usr/local/bin/restic-backup
  UNIT
}

resource "host_systemd_unit" "backup_timer" {
  name = "restic-backup.timer"

  content = <<-UNIT
    [Unit]
    Description=Run Restic backup every hour

    [Timer]
    OnCalendar=hourly
    Persistent=true

    [Install]
    WantedBy=timers.target
  UNIT
}
