# Python without uv, including Pants

Keep your existing Python build and GitHub Actions workflow. dispat can coordinate its releases without a uv
workspace. The integration depends on where distribution metadata comes from: a `pyproject.toml` version is a
different input from a Pants BUILD target or a plugin that reads a shared `VERSION` file.

## Identify the version source

Before configuring an edit, find the value that supplies each distribution's published metadata. It may be a literal
in a BUILD target, a shared `VERSION` file, or a repository plugin. Preserve custom metadata rules explicitly: a
generic `python -m build` command cannot replace them. Version a shared source once before packaging and keep the
build step separate from the upload step.

## Plain build and Twine

For distributions already described by a buildable `pyproject.toml`, use shared commands:

```json title="dispat.json"
{
  "scripts": {
    "build": "python -m build && python -m twine check --strict dist/*",
    "publish": "python -m twine upload dist/*"
  },
  "spaces": {
    "packages": {
      "path": "packages",
      "flow": {"build": "build", "publish": "publish"},
      "autoVersion": {"enabled": true}
    }
  }
}
```

Install the project’s Python and packaging tools in Actions first. Each package runs the shared commands in its own
folder. Use a fresh `dist/` directory per package and run; upload only the artifact inventory just validated. The
upload command above is a first-attempt example. Before a retry, reconcile accepted filenames and hashes with the
index as described in [the Python guide](./python.md#check-the-release-file-set).

The parent layout matters: dispat 1.10 requires package paths below the repository root. Do not claim that adding
`path: "."` wraps an unchanged root-only project. See [single-package layout](./single-package.md).

## Pants adapter

For two Python distributions, `src/core:dist` and `src/app:dist`, suppose the app imports the core package and each
BUILD file has a literal `version="1.0.0"` in its distribution metadata. The configuration is:

```json title="dispat.json"
{
  "concurrency": [1, 1],
  "scripts": {
    "build": "cd \"$RELEASE_ROOT\" && pants package \"$PANTS_TARGET\""
  },
  "spaces": {
    "python": {
      "path": "src",
      "flow": {"build": "build"},
      "autoVersion": {
        "enabled": true,
        "manifests": "none",
        "replace": [{
          "files": ["BUILD"],
          "find": "version=\"{previous}\"",
          "write": "version=\"{version}\""
        }]
      },
      "packages": {
        "core": {"env": {"PANTS_TARGET": "src/core:dist"}},
        "app": {
          "env": {"PANTS_TARGET": "src/app:dist"},
          "dependencies": [{"provider": "core", "keep": true}]
        }
      }
    }
  },
  "initials": {"core": "1.0.0", "app": "1.0.0"}
}
```

Set `RELEASE_ROOT` to the absolute checkout root. The shared command runs once per selected distribution, from the
root where Pants expects its configuration. Build concurrency is one. Pants retains its internal scheduling and
import inference; the explicit dispat dependency supplies release ordering and propagation. `dispat compute` cannot
infer this edge from BUILD files.

The literal replacement is deliberately specific. It does not edit arbitrary Python syntax or evaluate BUILD files.
For a repository plugin that reads a shared version source, update that source once before packaging and choose a
matching [shared version policy](../reference/releasing/versioning.md). Do not run independent package replacements
against the same shared file.

This configuration has no publish stage. Adapt the existing publisher to select only the current distribution’s
expected files from Pants’ shared `dist/`, validate their package/version and hashes, then upload them. Do not make
each package invocation upload every wheel in that shared folder. That would cross package completion boundaries.

## Keep the GitHub Actions release boundary

Keep the current toolchain setup, required tests, caches and credentials. Fetch full history and tags. In a serialized
release job, set `RELEASE_ROOT: ${{ github.workspace }}`, preview with `dispat status`, and run dispat after required
gates pass. Configure the publisher and artifact checks before enabling that production release step; the build-only
example above is not a complete publication workflow. See [dispat in CI](../reference/ci.md).

Removing Pants is a separate migration. First reproduce one distribution’s files, metadata, entry points, resources,
native artifacts and dependency declarations with another backend, then test a clean installation. pip’s download
cache does not replace Pants’ dependency inference or process-result cache. Keep existing caches while measuring
cold and warm builds. Follow the CI and cache guidance for the Pants release used by the repository.

Add further deliverables using [From one package to many](./one-to-many.md); their release graph can grow while each
package keeps its own build tool.
