resource "host_file" "zshrc" {
  path = "~/.zshrc"

  block {
    name    = "environment"
    content = <<-EOT
      export LESSHISTFILE=/dev/null
      export WORDCHARS=""
      export LANG="en_US.UTF-8"
      export LC_ALL="en_US.UTF-8"
      export HISTSIZE=1000000000
      export SAVEHIST=1000000000
    EOT
  }

  block {
    name = "path"
  }

  block {
    name    = "alias"
    content = <<-EOT
      alias y='pbcopy' p='pbpaste'
      alias rr='source ~/.zshrc'
      alias -g ...='../..'
      alias -g ....='../../..'
    EOT
  }

  block {
    name    = "options"
    content = <<-EOT
      setopt append_history
      setopt autopushd
      setopt extended_glob
      setopt hist_find_no_dups
      setopt hist_ignore_all_dups
      setopt hist_ignore_space
      setopt hist_reduce_blanks
      setopt hist_save_no_dups
      setopt inc_append_history
      setopt interactive_comments
      setopt share_history
    EOT
  }

  block {
    name    = "keybindings"
    content = <<-EOT
      bindkey -e
      bindkey '^[[1;3C' forward-word
      bindkey '^[[1;3D' backward-word
      bindkey '^[[1;5C' end-of-line
      bindkey '^[[1;5D' beginning-of-line
    EOT
  }

  block {
    name = "init"
  }
}

resource "host_file_block" "editor" {
  block   = host_file.zshrc.blocks.environment
  content = "export EDITOR=nvim"
}

resource "host_file_block" "git_aliases" {
  block = host_file.zshrc.blocks.alias

  content = <<-EOT
    alias ga='git add'
    alias gc='git commit -v'
    alias gl='git pull'
    alias gp='git push'
    alias gst='git status'
  EOT
}

resource "host_file_block" "starship" {
  block   = host_file.zshrc.blocks.init
  content = "eval \"$(starship init zsh)\""
}

resource "host_file" "gitconfig" {
  path = "~/.gitconfig"

  content = <<-EOT
    [user]
      name = Alice Example
      email = alice@example.com
    [core]
      editor = nvim
      autocrlf = input
      quotePath = false
    [init]
      defaultBranch = main
    [push]
      autoSetupRemote = true
  EOT
}
