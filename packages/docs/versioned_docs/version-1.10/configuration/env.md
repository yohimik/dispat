# Static env

Define fixed environment variables for your scripts using `env` objects. Put a single map at the top of your
configuration file to reach every script:

```yaml
env:
  NPM_CONFIG_REGISTRY: https://npm.corp.example
```

You can also add `env` objects to spaces and packages. dispat merges these three layers key by key, and the most local
definition wins. A key set in only one layer reaches every script under it, but a key set in multiple layers uses the
nearest value:

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

Scripts in the `core` package see all three variables: the registry, the Go flags, and `CGO_ENABLED`. Scripts in other
`libs` packages see only the first two.

Run-level hooks execute at the repository root with no package in view. They see only the top-level map because no
space or package applies to them.

You can also write an `env` map inside a [space folder's or package folder's own config file](./packages.md). These
files count as layers just like the main configuration. An in-folder file acts as a more local layer than the entry
that names it.

## Where else a variable can come from

Variables defined in `env` objects always win over the process environment and the [`.env` file](./dotenv.md). dispat
reads that file from the folder you run it in. If your configuration omits a variable, your scripts still receive it
from the environment or `.env` file.

## Values can reference other variables

Write `$NAME` or `${NAME}` to expand a value before the script runs. dispat resolves these against the script's
computed [`DISPAT_*` variables](../reference/environment.md) first, and the process environment second:

```yaml
env:
  CUSTOM_TAG: custom_$DISPAT_VERSION
```

Each package gets its own version for its scripts. The `core` package at 1.4.0 sees `CUSTOM_TAG=custom_1.4.0`. dispat
handles this expansion directly because exported variables never pass through a shell.

Write `$$` to escape a literal dollar sign. An unknown name expands to nothing, just like in a shell.

## Two things you cannot do

Keys keep the exact case you write them in, like every config map key, and here that is not a nicety: `PATH` and
`Path` are two variables. Elsewhere the spelling is preserved and the matching folds, so a script, space or package
name is reached however it is written; an environment variable is only ever the key itself.

dispat rejects two keys that differ only in case because it cannot tell which one you meant. It asks this of every
object it reads, and of an `env` object once more, because the layers merge key by key.

You cannot override a computed variable with a static one. The `DISPAT_` prefix is reserved, so setting a key that
starts with it throws a configuration error instead of failing quietly. This strictness means your scripts can read
`DISPAT_VERSION` and trust it.
