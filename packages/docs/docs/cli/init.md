# The init command

`dispat init` writes a starter config file into `--root` (`dispat.json`, or `dispat.yaml` / `dispat.toml` with
`--format`) and exits. An existing file is never overwritten; that is an error. So is a `--root` that is not a git
repository root (no `.git`): the config establishes the effective monorepo root, so it belongs next to `.git`. Needs no
config file.

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--format`            | `json`      | `init` only: the config file format to write (`json`, `yaml` or `toml`).                                                                                                                               |
