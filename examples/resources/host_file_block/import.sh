# Import as "<path>:<block name>:<block id>". The block ID is the `hfb-...`
# identifier recorded for the block in the provider runtime state; find it in
# <runtime_dir>/host_files/*.json. New configurations normally use
# ~/.local/state/terraform-provider-host/host_files; legacy configurations may
# still use ./.terraform-provider-host/host_files next to the Terraform working dir.
terraform import host_file_block.git_aliases '~/.zshrc:alias:hfb-0123456789abcdef0123456789abcdef'
