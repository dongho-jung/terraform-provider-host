resource "host_package_brew" "font_inconsolata_nerd_font" {
  name         = "font-inconsolata-nerd-font"
  package_type = "cask"
}

# Homebrew quarantines every cask artifact it downloads, so the fonts it copies
# into ~/Library/Fonts stay unregistered until the attribute is gone and the
# font daemon rescans the directory.
resource "host_mac_font" "inconsolata_nerd_font" {
  path = "~/Library/Fonts"

  depends_on = [host_package_brew.font_inconsolata_nerd_font]
}

# Activate one file, and reuse the PostScript name macOS resolves it by instead
# of hard-coding it in the preference that selects the font.
resource "host_mac_font" "terminal" {
  path = "~/Library/Fonts/InconsolataNerdFontMono-Regular.ttf"
}

output "terminal_font" {
  value = "${one(host_mac_font.terminal.postscript_names)} 13"
}
