# The `.env` file

dispat reads `.env` from your current folder before doing anything else. It adds these variables to its own
environment. Every script, hook, and login command inherits them. dispat inherits them too.

```sh
# .env
NPM_TOKEN=npm_xxxxxxxx
GITHUB_TOKEN=ghp_xxxxxxxx
```

```yaml
scripts:
  publish: npm publish --access public   # reads NPM_TOKEN from the environment
```

You do not need this file. Most repositories have none, and that is not an error. The format is standard: one
`NAME=value` per line, `#` for comments, quotes for spaces, and an optional `export` prefix.

dispat reads the file from the **current directory**. This is the folder your shell is in, not `--root` and not the
monorepo root. The file belongs to the person running the command, not the repository being released.

## What wins

Three sources can set a variable for your scripts. From weakest to strongest:

| Source | Beaten by | Why |
|--------|-----------|-----|
| `.env` | everything below | It fills in what nothing else sets |
| The environment you ran dispat in | the config's `env` | A value your CI job exports is never replaced by a file in the repository |
| The config's [`env`](./env.md) objects | the computed `DISPAT_*` variables | The configuration is the repository's own statement about its scripts |

A token exported by a CI job wins over a `.env` someone committed by accident. A variable pinned in the config wins
over both. The computed `DISPAT_VERSION` and its siblings always win.

## Naming other files

Pass `--env-file` to read a specific file instead of `./.env`:

```sh
dispat release --env-file .env.ci
```

You can repeat the flag. A later file wins over an earlier one:

```sh
dispat release --env-file .env.shared --env-file .env.ci
```

Naming a file turns the default off, so dispat skips `./.env`. A missing named file stops the run. This works the same
way a misspelled `--config` does. Asking for a file and silently getting nothing is worse than stopping.

## Dispat's own variables

The file reaches the variables dispat reads for itself. You can set the GitHub token for
[`github`](./records.md#github), `DISPAT_UPDATE_CHECK`, `DISPAT_UNSAFE_DISABLE_LOCK`, and any variable a record line or
an `env` value refers to with `$NAME`. Keeping `GITHUB_TOKEN` in `.env` instead of a shell profile works perfectly.

The config's `env` object refuses keys starting with `DISPAT_`. A computed variable could never be shadowed anyway. The
`.env` file allows them, because reaching those switches is the point. The computed variables still win inside package
scripts.

## Secrets

Tokens live in `.env` files. dispat never logs a value from one. Run with `--log-level debug` to see how many files
were read. Run with `--log-level trace` to see the keys and whether each was added or already set:

```
TRC variable added from an environment file key=NPM_TOKEN
TRC the environment already sets this variable key=GITHUB_TOKEN
DBG environment files read added=1 files=[".env"] kept=1
```

Add `.env` to your `.gitignore`. dispat does not need this file committed. The precedence rules assume your real
secrets come from your CI provider.
