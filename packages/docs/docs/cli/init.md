# The init command

Start a new setup with `dispat init`. It writes `dispat.json` in `--root`, which defaults to your current directory.
Use `--format yaml` or `--format toml` if you prefer one of those formats.

You do not need an existing config file to run this command. Your `--root` must be a git repository root containing a
`.git` directory. dispat places the config file next to `.git` to establish the effective monorepo root, and fails if
the directory is missing.

If the target directory already contains a config file, the command stops with an error and leaves it unchanged.

## Flags

In addition to the [global flags](./README.md#global-flags):

### `--format`

The default is `json`. Sets the config file format to write (`json`, `yaml`, or `toml`). This flag applies to `init`
only.
