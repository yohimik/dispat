# The `.env` file

Before dispat does anything else, it reads `.env` from the folder you run it in and adds those variables to its own
environment. Every script, hook and login command it runs inherits them, and so does dispat itself.

```sh
# .env
NPM_TOKEN=npm_xxxxxxxx
GITHUB_TOKEN=ghp_xxxxxxxx
```

```yaml
scripts:
  publish: npm publish --access public   # reads NPM_TOKEN from the environment
```

The file is optional. Most repositories have none, and that is not an error. The format is the usual one:
`NAME=value` per line, `#` comments, quotes when a value has spaces, and an optional `export` in front.

The file is read from the **current directory**, the folder your shell is in, not from `--root` and not from the
monorepo root. It belongs to whoever is running the command rather than to the repository being released.

## What wins

Three things can set a variable a script sees. From weakest to strongest:

| Source | Beaten by | Why |
|--------|-----------|-----|
| `.env` | everything below | It fills in what nothing else said |
| The environment you ran dispat in | the config's `env` | A value your CI job exported is never replaced by a file in the repository |
| The config's [`env`](./env.md) objects | the computed `DISPAT_*` variables | The configuration is the repository's own statement about its scripts |

So a token exported by a CI job wins over a `.env` someone committed by accident, and a variable the config pins wins
over both. The computed `DISPAT_VERSION` and its siblings always win, as they always do.

## Naming other files

`--env-file` reads a file of your choosing instead of `./.env`:

```sh
dispat release --env-file .env.ci
```

It is repeatable, and a later file wins over an earlier one:

```sh
dispat release --env-file .env.shared --env-file .env.ci
```

Naming files turns the default off, so `./.env` is not read as well. And a file you name that is not there stops the
run, the same way a misspelled `--config` does: asking for a file and silently getting none of it is worse than
stopping.

## Dispat's own variables

Because the file is read into the environment, it also reaches the variables dispat reads for itself: the GitHub token
of [`github`](./records.md#github), `DISPAT_UPDATE_CHECK`, `DISPAT_UNSAFE_DISABLE_LOCK`, and any variable a record
line or an `env` value refers to with `$NAME`. Keeping `GITHUB_TOKEN` in `.env` and never in a shell profile is a
supported way to work.

The config's own `env` object refuses keys starting with `DISPAT_`, since a computed variable could never be shadowed
anyway. `.env` allows them, because reaching those switches is the point. The computed variables still win inside
package scripts.

## Secrets

A `.env` is where tokens live, so dispat never logs a value from one. `--log-level debug` reports how many files were
read, and `--log-level trace` names the keys and whether each one was added or already set:

```
TRC variable added from an environment file key=NPM_TOKEN
TRC the environment already sets this variable key=GITHUB_TOKEN
DBG environment files read added=1 files=[".env"] kept=1
```

Add `.env` to your `.gitignore`. Nothing in dispat requires the file to be committed, and the precedence rules above
are built on the assumption that the real secrets come from your CI provider.
