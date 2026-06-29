resource "host_file" "zshrc" {
  path = "~/.zshrc"

  content = trimspace(<<-EOT
    export EDITOR=nvim
    eval "$(starship init zsh)"
  EOT
  )
}

resource "host_file" "zshrc_sections" {
  path   = "~/.zshrc"
  render = "clean"

  block = {
    options = {
      priority = 10
      content = trimspace(<<-EOT
        setopt autocd
        bindkey -v
      EOT
      )
    }
    alias = {
      priority = 20
    }
    functions = {
      priority = 30
    }
  }
}

resource "host_file_block" "foo_alias" {
  file_block = host_file.zshrc_sections.block["alias"]
  content    = "alias foo=foobar"
}

resource "host_file_block" "bar_alias" {
  file_block = host_file.zshrc_sections.block["alias"]
  content    = "alias bar=barbaz"
}

resource "host_file_block" "foo_function" {
  file_block = host_file.zshrc_sections.block["functions"]
  content    = "foo() { echo foo }"
}

resource "host_file_block" "bar_function" {
  file_block = host_file.zshrc_sections.block["functions"]
  content    = "bar() { echo bar }"
}
