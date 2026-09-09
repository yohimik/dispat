# The init command

Run `dispat init` to write a starter config file into your `--root` directory. dispat creates a `dispat.json` file and
exits. Pass the `--format` flag to generate a `dispat.yaml` or `dispat.toml` file instead.

You do not need an existing config file to run this command. Your `--root` must be a git repository root containing a
`.git` directory. dispat places the config file next to `.git` to establish the effective monorepo root, and fails if
the directory is missing.

dispat never overwrites your work. The command throws an error if a config file already exists in the target directory.

## Flags

In addition to the [global flags](./README.md#global-flags):

### `--format`

The default is `json`. Sets the config file format to write (`json`, `yaml`, or `toml`). This flag applies to `init`
only.
