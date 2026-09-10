# Public ecosystem verification

The documentation audit covers 21 native ecosystem identifiers, with one pinned public manifest each. It does not
claim to build all upstream repositories or test every one of the 36 supported formats. `cases.json` records source
commits, SHA-256 hashes, writer arguments and scanner/writer expectations. `results.json` records the completed local
run and the additional Python artifact checks. Test versions are used only in temporary copies; nothing is uploaded.

Run from the repository root with Python 3 and dispat 1.10.0 on PATH:

```sh
python3 packages/docs/verification/ecosystems/verify.py --output /tmp/ecosystems.json
python3 packages/docs/verification/ecosystems/one-to-many.py
```

The first command fetches raw manifests only. It checks exact readback, changes, expected no-ops, preservation of
indirections, and idempotence. It never executes source from an upstream repository. The second creates a disposable
Git repository and verifies the one-package baseline and expanded release plan without building or publishing.

## Pants 2.17.0

`pants217/` is a small authored two-distribution fixture inspired by the BUILD-driven public layouts described in
the Python/Pants guide. It is not copied from either StackStorm or Backend.AI and does not load their plugins.
The Dockerfile pins Python's base image digest and installs Pants 2.17.0. Use Linux amd64 (emulated on an arm64 host).
The legacy pip-installed Pants launcher emits an upstream deprecation warning; this is intentional historical testing.

From the repository root, copy the fixture to a disposable folder:

```sh
fixture=$(mktemp -d)
cp -R packages/docs/verification/ecosystems/pants217/. "$fixture/"
docker build --platform linux/amd64 -t dispat-pants217-verification "$fixture"
cd "$fixture"
git init -q
git config user.name Verification
git config user.email verification@example.invalid
git add .
git commit -qm 'chore: initialize fixture'
git commit --allow-empty -qm 'fix(core)^: exercise package version handoff'
dispat status --log-format json
dispat autoversion --log-format json
```

Both packages must plan `1.0.0 -> 1.0.1`, and both BUILD version literals must become `1.0.1`. Download the matching
Linux binary to run the same shared build command inside the container:

```sh
gh release download services/dispat/v1.10.0 --repo yohimik/dispat \
  --pattern dispat-linux-amd64 --dir "$fixture"
chmod +x "$fixture/dispat-linux-amd64"
docker run --rm --platform linux/amd64 -v "$fixture:/work" -w /work \
  -e RELEASE_ROOT=/work dispat-pants217-verification \
  /work/dispat-linux-amd64 run build --since all --log-format json
```

Assert the log finishes with `ran=2, failed=0`, with core started before app. `dist/` must contain a wheel and sdist
for each distribution at `1.0.1`. Inspect the app wheel's METADATA for `Requires-Dist: example-core (==1.0.1)`, then
exercise the installed artifacts in another clean container:

```sh
docker run --rm --platform linux/amd64 -v "$fixture:/work" -w /work \
  dispat-pants217-verification sh -ec '
    python -m pip install --no-index --find-links=dist example-app==1.0.1
    python -c "from app import main; assert main() == 42"
  '
```

Each build invocation selects one Pants distribution, but Pants owns a shared `dist/`. A real publisher must select
and validate only the current package's inventory. This fixture intentionally has no publisher and tests no registry.
It also does not benchmark caches or show that replacing Pants is faster.

## PyPA build and Twine without uv

Clone `pypa/sampleproject` at `621e4974ca25ce531773def586ba3ed8e736b3fc` into a temporary directory, export its tracked
source with `git archive`, and extract it as `packages/sample/` inside a separate disposable Git repository. This
preserves the upstream source layout within a package; dispat does not support a package path of `.`.

Configure that parent repository with:

```json
{
  "spaces": {"packages": {"path": "packages", "autoVersion": {"enabled": true}}},
  "initials": {"sample": "4.0.0"}
}
```

Commit the fixture, then an empty `fix(sample): verify build without uv` commit. Run `dispat autoversion` and assert
that `packages/sample/pyproject.toml` now contains `version = "4.0.1"`. Mount the parent as `/work` and build in the
same Python 3.12 Linux arm64 image used for the recorded run:

```sh
# fixture is the absolute path to the disposable parent repository.
docker run --rm --platform linux/arm64 -v "$fixture:/work" -w /work/packages/sample \
  python:3.12-slim@sha256:78387bc3881b8273120a12ebe6c1ab22b018ccc2c9adf565ae1ac9b536e184ea \
  sh -ec '
    python -m pip install build==1.3.0 twine==6.2.0
    python -m build
    python -m twine check --strict dist/*
    python -m pip install --no-deps dist/*.whl
    python -c "from importlib.metadata import version; from sample.simple import add_one; assert version(\"sampleproject\") == \"4.0.1\"; assert add_one(41) == 42"
  '
```

The source's build backend requirements are resolved by PyPA build in isolation; transitive tool dependencies are
not frozen by these commands. `results.json` artifact hashes identify the measured outputs, not a promise of
byte-identical future wheels/sdists. Use the declared version, metadata and smoke assertions to check a new run.
