# Import as "<path>:<block name>:<block id>". The block ID is the `hfb-...`
# identifier recorded for the block in the provider runtime state; find it in
# <runtime_dir>/host_files/*.json. The default location is
# ~/.local/state/terraform-provider-host/host_files.
terraform import host_file_block.git_aliases '~/.zshrc:alias:hfb-0123456789abcdef0123456789abcdef'
