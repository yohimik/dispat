# Static env

Use `env` for environment variables your scripts should always receive. A top-level map applies to every script:

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

Environment keys keep their exact spelling. `PATH` and `Path` are different variable names, unlike script, space, and
package names, which dispat matches without regard to case.

You still cannot declare two keys that differ only by case. dispat rejects that ambiguity both when reading the
configuration and when merging the `env` layers.

The `DISPAT_` prefix is reserved for computed variables. Declaring one in `env` causes a configuration error, so a static
setting cannot replace the version or stage data your scripts receive.
