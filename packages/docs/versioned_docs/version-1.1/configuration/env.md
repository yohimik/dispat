# Static env

The `env` objects add fixed environment variables to the scripts dispat runs. The simplest use is one map at the top of
the file, reaching everything:

```yaml
env:
  NPM_CONFIG_REGISTRY: https://npm.corp.example
```

Spaces and packages can add their own, and the three layers merge key by key with the most local one winning. A key set
in only one layer reaches every script under it; a key set in two is decided by the nearer:

```yaml
env:
  NPM_CONFIG_REGISTRY: https://npm.corp.example   # every script
spaces:
  libs:
    env:
      GOFLAGS: -mod=mod                            # every script of the libs packages
packages:
  core:
    env:
      CGO_ENABLED: "1"                             # core's scripts only
```

A package's scripts here see all three: the registry, the Go flags, and `CGO_ENABLED`. The other packages of `libs` see
the first two. The run-level hooks, which execute at the repository root with no package in view, see the top-level map
alone, because no space or package applies to them.

Where you can write an `env` map, you can write it in a [space folder's or package folder's own config file](./packages.md)
too. Those count as layers like any other, the in-folder file being more local than the entry that names it.

## Where else a variable can come from

The `env` objects are the configuration's own statement, so they win over the process environment and over the
[`.env` file](./dotenv.md) dispat reads from the folder you run it in. A variable nothing here mentions still reaches
your scripts if the environment or that file defines it.

## Values can reference other variables

`$NAME` and `${NAME}` in a value are expanded before the script runs, against the script's computed
[`DISPAT_*` variables](../reference/environment.md) first and the process environment second:

```yaml
env:
  CUSTOM_TAG: custom_$DISPAT_VERSION
```

Each package's scripts get their own version, so `core` at 1.4.0 sees `CUSTOM_TAG=custom_1.4.0`. dispat does this
expansion itself, because exported variables never pass through a shell. Write `$$` for a literal dollar sign; an
unknown name expands to nothing, the same as in a shell.

## Two things you cannot do

Keys keep the exact case you write them in, which is worth knowing because the rest of the configuration is
case-insensitive: script names, space names and package names all match regardless of case. Environment variables do
not, so dispat reads the `env` objects back from your file to preserve their spelling. Two keys differing only in case
are rejected, since there would be no way to tell which one you meant.

A static variable also cannot override a computed one. The `DISPAT_` prefix is reserved, and setting a key that starts
with it is a configuration error rather than something quietly ignored. That is what lets a script read
`DISPAT_VERSION` and trust it.
