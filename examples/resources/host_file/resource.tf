resource "host_file" "zshrc" {
  path = "~/.zshrc"

  content = <<-EOT
    export EDITOR=nvim
    eval "$(starship init zsh)"
  EOT
}

resource "host_file" "zshrc_blocks" {
  path = "~/.zshrc"

  block {
    name    = "options"
    content = <<-EOT
      setopt autocd
      bindkey -v
    EOT
  }

  block {
    name = "alias"
  }

  block {
    name = "functions"
  }
}

resource "host_file_block" "foo_alias" {
  block   = host_file.zshrc_blocks.blocks.alias
  content = "alias foo=foobar"
}

resource "host_file_block" "bar_alias" {
  block   = host_file.zshrc_blocks.blocks.alias
  content = "alias bar=barbaz"
}

resource "host_file_block" "foo_function" {
  block   = host_file.zshrc_blocks.blocks.functions
  content = <<-EOT
    foo() { echo foo }
  EOT
}

resource "host_file_block" "bar_function" {
  block   = host_file.zshrc_blocks.blocks.functions
  content = <<-EOT
    bar() { echo bar }
  EOT
}
