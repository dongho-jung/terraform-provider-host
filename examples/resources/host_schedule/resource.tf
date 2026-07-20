resource "host_dir" "projects" {
  path = "~/projects"
  mode = "0755"
}

resource "host_git_repo" "shell_history" {
  url  = "git@github.com:example/shell-history.git"
  path = "${host_dir.projects.path}/shell-history"

  delete_on_destroy = false
}

# The provider verifies and repairs this schedule's generated runtime files and
# exact cron entry on later applies.
resource "host_schedule" "shell_history_git_auto_commit" {
  schedule = "*/30 * * * *"
  shell    = "/bin/zsh"

  command = <<-EOT
    set -euo pipefail

    cd "${host_git_repo.shell_history.path_resolved}" || exit 1
    export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

    branch="main"
    remote="origin"

    git fetch "$remote" "$branch"
    if ! git diff --quiet "HEAD..$remote/$branch"; then
      git pull --rebase --autostash "$remote" "$branch"
    fi

    if [[ -n "$(git status --porcelain)" ]]; then
      git add -A
      git commit -m "Auto update: $(date '+%Y-%m-%d %H:%M:%S')"
      git push "$remote" "$branch"
    fi
  EOT
}
