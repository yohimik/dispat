# Getting started

## Install

```sh
git clone <your fork or vendor path>
cd dispat
go mod tidy
go build -o dispat .
go test ./...
```

## First configuration

Create `dispat.json` at your monorepo root (YAML or TOML work too — pass `--config dispat.yaml`). Minimal example:

```json
{
  "scripts": {
    "build": "npm ci && npm run build",
    "publish": "npm publish"
  },
  "spaces": {
    "libs": {
      "path": "packages/libs",
      "buildScript": "build",
      "publishScript": "publish"
    }
  }
}
```

Every direct sub-folder of `packages/libs` is now a package named after its folder. Declare relations between packages
under `dependencies` so bumps propagate and ordering is enforced:

```json
{
  "dependencies": [{ "consumer": "app", "provider": "core" }]
}
```

Beyond scripts and spaces there are optional top-level knobs — `concurrency` (`[build, publish]` budgets), `changelog`
and `github` (customise or disable the release records), `initials` (baseline versions for packages without a usable
tag), `commit` and `push` (end-of-run release commit and pushing, disabled by default) — and per-space options
`isBuildWaitingPublish`, `revertOnFail` and `versionScript`. All are covered in
[Configuration & CLI](configuration.md). Annotated full examples: [`dispat.example.json`](../dispat.example.json),
[`dispat.example.yaml`](../dispat.example.yaml).

## Commit convention

Name the package in the commit scope: `fix(core): close leak` (patch), `feat(core): add streaming` (minor),
`BREAKING CHANGE(core): drop old API` or `feat(core)!: new API` (major). Commits without a recognized form or scope are
ignored for versioning.

## Commands

```sh
./dispat status               # print the project graph and planned versions; changes nothing
./dispat                      # release (default command): full pipeline
./dispat release --root path  # same, explicit
./dispat --concurrency 4,2    # override build/publish parallelism
./dispat --log-format json    # machine-readable logs for CI
./dispat --log-level debug    # more verbose output
```

## Running in CI (GitHub Actions example)

```yaml
jobs:
  release:
    runs-on: ubuntu-latest
    permissions:
      contents: write   # create tags and releases
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0      # full history: dispat reads tags and commit ranges
      - uses: actions/setup-go@v5
      - run: go run . --log-format json
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          # GITHUB_REPOSITORY is set automatically by Actions
      - run: git push origin --tags   # publish the tags dispat created
```

Notes:

- `fetch-depth: 0` matters — without full history and tags the planner cannot find previous releases.
- By default dispat creates tags locally; push them after a successful run (as above). Alternatively enable
  `"commit": {"enabled": true, "push": true}` in the config: dispat then creates one release commit (changelogs +
  manifest changes), places the tags on it and pushes everything itself — drop the manual `git push` step. This
  requires a checked-out branch (`actions/checkout` with a `ref`), and remote access is verified before any work
  starts.
- Known limitations: shallow clones are not detected (always use `fetch-depth: 0`), and concurrent dispat runs on the
  same checkout are not guarded by a lock — serialize release jobs in CI.
- GitHub releases are created via the API (enabled by default). In release-commit mode their body always documents the
  release commit SHA and tag. With `commit.push` enabled they are created after the push and pinned to the release
  commit via `target_commitish`; without it, GitHub creates the tag ref at the default branch head until you push (the
  true commit/tag stay recorded in the body) — or disable releases with `"github": {"enabled": false}`.
- Exit code is non-zero when any package fails, so the job fails visibly while unaffected packages still released.
