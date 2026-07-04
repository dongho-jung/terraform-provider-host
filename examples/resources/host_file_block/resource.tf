resource "host_file" "zshrc" {
  path = "~/.zshrc"

  block {
    name = "environment"
  }

  block {
    name = "alias"
  }

  block {
    name = "init"
  }
}

resource "host_file_block" "editor" {
  block   = host_file.zshrc.blocks.environment
  content = "export EDITOR=nvim"
}

resource "host_file_block" "eza_aliases" {
  block = host_file.zshrc.blocks.alias

  content = <<-EOT
    alias ls='eza'
    alias l='eza -lbF --git'
    alias ll='eza -la --git'
  EOT
}

resource "host_file_block" "starship_init" {
  block   = host_file.zshrc.blocks.init
  content = "eval \"$(starship init zsh)\""
}
