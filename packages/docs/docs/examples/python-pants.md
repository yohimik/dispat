# Python without uv, including Pants

Keep your existing Python build and GitHub Actions workflow. dispat can coordinate its releases without a uv
workspace. The integration depends on where distribution metadata comes from: a `pyproject.toml` version is a
different input from a Pants BUILD target or a plugin that reads a shared `VERSION` file.

## Start with a comparable public repository

The following source revisions were inspected locally on 10 September 2026:

| Repository | Build and version inputs | Consequence |
| --- | --- | --- |
| [StackStorm st2, `9824de4`](https://github.com/StackStorm/st2/blob/9824de4dfd0c869869e310dee729308f398ad83a/pants.toml) | Pants 2.25.0 and [custom distribution/version rules](https://github.com/StackStorm/st2/blob/9824de4dfd0c869869e310dee729308f398ad83a/pants-plugins/release/rules.py). | Retain or replace those rules explicitly. A generic `python -m build` command cannot supply their metadata by itself. |
| [Backend.AI 24.03.0, `89324e0`](https://github.com/lablup/backend.ai/blob/89324e0c3a30289aaaed7672ae1e9b5fa5d74b27/pants.toml) | Pants 2.21.0.dev4; [distribution targets](https://github.com/lablup/backend.ai/blob/89324e0c3a30289aaaed7672ae1e9b5fa5d74b27/src/ai/backend/common/BUILD); a [setup plugin reading root VERSION](https://github.com/lablup/backend.ai/blob/89324e0c3a30289aaaed7672ae1e9b5fa5d74b27/tools/pants-plugins/setupgen/register.py); [Actions builds wheels and uploads with Twine](https://github.com/lablup/backend.ai/blob/89324e0c3a30289aaaed7672ae1e9b5fa5d74b27/.github/workflows/default.yml). | Version the shared source before packaging and preserve its generated metadata. Distinguish the build step from the upload step. |

Neither revision runs Pants 2.17.0. Their sources establish useful architecture comparisons, not compatibility with
that version. The separate fixture below actually ran Pants 2.17.0. It does not reproduce either repository’s custom
plugins or full build.

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

A local check used [PyPA sampleproject at `621e497`](https://github.com/pypa/sampleproject/tree/621e4974ca25ce531773def586ba3ed8e736b3fc).
Its source was placed in `packages/sample` in a disposable parent repository. dispat 1.10.0 reconciled `4.0.0` to
`4.0.1`; Python 3.12, build 1.3.0 and Twine 6.2.0 built and checked both artifacts. A clean container installed the
wheel and verified the installed version and `sample.simple.add_one(41) == 42`. No upload ran. The native scanner
also passed on the original manifest; see [all ecosystem checks](./open-source.md).

The parent layout matters: dispat 1.10 requires package paths below the repository root. Do not claim that adding
`path: "."` wraps an unchanged root-only project. See [single-package layout](./single-package.md).

## A locally exercised Pants 2.17 adapter

The [small fixture](https://github.com/yohimik/dispat/tree/main/packages/docs/verification/ecosystems/pants217)
contains two Python distributions, `src/core:dist` and `src/app:dist`. The app imports the core package. Each BUILD
file has a literal `version="1.0.0"` in its `python_artifact` declaration. Its configuration is:

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

The fixture’s literal replacement is deliberately specific. It does not edit arbitrary Python syntax, evaluate
BUILD files, or support Backend.AI’s shared-version plugin. For that architecture, update the shared version source
once before packaging and choose a matching [shared version policy](../reference/releasing/versioning.md). Do not
run independent package replacements against the same root VERSION file.

Locally, `fix(core)^: exercise package version handoff` planned `1.0.1` for both distributions. `dispat autoversion`
updated their BUILD literals. Pants 2.17.0 on Linux amd64 with Python 3.9 built:

```text
example-core-1.0.1.tar.gz
example_core-1.0.1-py3-none-any.whl
example-app-1.0.1.tar.gz
example_app-1.0.1-py3-none-any.whl
```

The app wheel’s metadata declared `Requires-Dist: example-core (==1.0.1)`. A clean container installed the app and
core from this local artifact directory and executed `app.main() == 42`. The
[verification instructions](https://github.com/yohimik/dispat/blob/main/packages/docs/verification/ecosystems/README.md)
retain the fixture, pinned container base and reproduction commands. Pants emitted deprecation warnings for its
legacy pip-installed launcher and unspecified import-parser option; these were not packaging failures. This is a
historical-version check, not a recommendation to adopt that bootstrap for newer Pants.

This configuration has no publish stage. Adapt the existing publisher to select only the current distribution’s
expected files from Pants’ shared `dist/`, validate their package/version and hashes, then upload them. Do not make
each package invocation upload every wheel in that shared folder. That would cross package completion boundaries.

## Keep the GitHub Actions release boundary

Keep the current toolchain setup, required tests, caches and credentials. Fetch full history and tags. In a serialized
release job, set `RELEASE_ROOT: ${{ github.workspace }}`, preview with `dispat status`, and run dispat after required
gates pass. Configure the publisher and artifact checks before enabling that production release step; the build-only
fixture above is not a complete publication workflow. See [dispat in CI](../reference/ci.md).

Removing Pants is a separate migration. First reproduce one distribution’s files, metadata, entry points, resources,
native artifacts and dependency declarations with another backend, then test a clean installation. pip’s download
cache does not replace Pants’ dependency inference or process-result cache. Keep existing caches while measuring
cold and warm builds; no speedup was measured by this verification. Pants 2.17’s
[CI guidance](https://github.com/pantsbuild/pants/blob/c0089a1cd567111a4e5aa0cd25c2058aa8a354a4/docs/markdown/Using%20Pants/using-pants-in-ci.md)
explains the cache choices available to that version.

Add further deliverables using [From one package to many](./one-to-many.md); their release graph can grow while each
package keeps its own build tool.
