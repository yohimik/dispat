# A Python monorepo

Distributions in one repository, built and uploaded with `uv`, with `pyproject.toml` and the `requirements.txt` beside
it both kept current by dispat.

## The layout

```
packages/core/pyproject.toml    acme-core 1.2.0
packages/app/pyproject.toml     acme-app 0.4.1, depends on acme-core
packages/app/requirements.txt   the pins the container image installs
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "scripts": {
    "build": "uv build",
    "publish": "uv publish",
    "lock": "uv lock"
  },
  "spaces": {
    "packages": {
      "path": "packages",
      "flow": {"build": "build", "publish": "publish"},
      "autoVersion": {"enabled": true, "syncLock": ["lock"]}
    }
  }
}
```

Poetry, PDM, Hatch, and plain `build` plus `twine`, all drop into the same two scripts; nothing above is uv-specific
except the commands themselves.

One option to know about before you need it: `autoVersion.manifests` defaults to `root`, which reconciles the
manifests directly in the package folder. Set it to `all` when a package keeps a second manifest deeper down, such as
a `deploy/requirements.txt` next to a Dockerfile.

## A release

`acme-app` has no commits of its own here. It moves because the commit on `core` asked for its dependants with `^`:

```console
$ git commit -m "feat(core)^: stream responses"
$ dispat status
12:39:15 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=packages version="1.2.0 -> 1.3.0"
12:39:15 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=app reason="propagated from core" space=packages version="0.4.1 -> 0.4.2"
12:39:15 INF release plan ready held=0 packages=2 releasing=2
```

Without the `^`, `core` would release alone and `app` would stay where it is until it next has a reason of its own.
[Propagation is opt-in](../concepts.md#propagation-is-opt-in) so that one library fix does not rebuild the world.

## What the version stage does

```console
$ dispat autoversion
12:39:15 INF manifest reconciled manifest=pyproject.toml package=core ranges=0 stage=version versionWritten=true
12:39:15 INF manifest reconciled manifest=pyproject.toml package=app ranges=1 stage=version versionWritten=true
12:39:15 INF manifest reconciled manifest=requirements.txt package=app ranges=1 stage=version versionWritten=false
12:39:15 INF auto-versioning finished failed=0 ran=2 skipped=0 stage=autoversion
```

Three files, two packages, one pass. `versionWritten=false` on the requirements file is not a failure: that format has
no version of its own to write, only pins to reconcile. The result:

```toml
[project]
name = "acme-app"
version = "0.4.2"
dependencies = [
    "acme-core==1.3.0",
    "httpx>=0.27.0",
]
```

```
# runtime pins for the container image
acme-core==1.3.0
httpx==0.27.2
```

Only the version text moved. The comment, the ordering and `httpx` are exactly as they were, which is what makes the
release commit reviewable.

## What dispat reads and writes

```console
$ dispat scanner
packages/app/pyproject.toml  python  acme-app@0.4.1
  dependencies     acme-core  ==1.2.0
  dependencies     httpx      >=0.27.0
  devDependencies  pytest     >=8.3.0
packages/app/requirements.txt  python
  dependencies  acme-core  ==1.2.0
  dependencies  httpx      ==0.27.2
packages/core/pyproject.toml  python  acme-core@1.2.0
  dependencies  httpx  >=0.27.0
3 manifest(s), 6 dependency declaration(s)
```

- **`pyproject.toml`** is read as PEP 621 first (`[project]`), falling back to `[tool.poetry]`. Optional dependencies
  become optional ones, and both PEP 735 `[dependency-groups]` and non-main Poetry groups become dev dependencies.
- **Requirements files** match by whole words, so `requirements.txt`, `dev-requirements.txt` and `requirements-ci.txt`
  all count and a file with `dev` or `test` in its name is read as dev dependencies. An editable local install,
  `-e ./core`, is reported as a link to that folder.
- **Names are normalised** the way PyPI normalises them (PEP 503), so `Acme_Core` and `acme-core` are one package.
- **Python ranges are written as `==X.Y.Z`.** A `caret` or `tilde` policy is an npm idea; the Python writers use the
  spelling the ecosystem actually resolves.

## Building against the package next door

`--link` writes a `[tool.uv.sources]` path entry, and the empty form removes it:

```sh
dispat autowriter --since all --link-local     # develop against the working tree
dispat autowriter --since all --unlink-local   # before anything is uploaded
dispat scanner --verify-unlinked               # E215 if one survived
```

## Worth knowing

- **A version on PyPI cannot be replaced.** Test in the build stage, upload in the publish stage, and let the ordering
  guarantee that a dependency is on the index before its consumer needs it.
- **`uv lock` runs after the manifests are reconciled.** That is what `syncLock` means: the lock follows the manifest
  rather than choosing versions of its own.
- **A workspace-wide lock file lives at the repository root**, outside every package folder. List it under
  [`commit.include`](../configuration/records.md#commit) so the release commit carries it.
- **Publishing needs credentials, once per space.** A token in the environment is enough for `uv publish`; if your
  registry needs a login command, the [`flow.login` slot](./login.md) runs it once per space per run.

## See also

- [autoVersion](../configuration/autoversion.md) for `manifests`, `match` and `syncLock` in full.
- [An npm monorepo](./npm.md) for the same shape in a different ecosystem.
- [A Docker image chain](./docker.md) if the requirements file exists to feed an image.
