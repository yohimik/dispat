# @dispat/bin <img alt="dispat logo" align="right" width="128" height="128" src="https://raw.githubusercontent.com/yohimik/dispat/main/imgs/logo.png" />

**dispat** reads your conventional commits, computes the next package versions, and runs build and publish commands in dependency order. Preview the release with `dispat status` before running it.

`@dispat/bin` installs the native Go executable and exposes the `dispat` command. Use it with a single Node application, an npm, pnpm, or Yarn monorepo, or several repositories joined through a control repository. Your existing package manager, build tools, and deployment commands remain part of the workflow.

[Documentation](https://dispat.dev/) · [GitHub](https://github.com/yohimik/dispat) · [Discord](https://discord.gg/83PwVSCCmk)

## Install

For a global command:

```sh
npm install --global @dispat/bin
dispat --version
```

For a project, install dispat once at the repository root and commit the package manager's lockfile:

| Package manager | Add to the root development dependencies | Run a release preview |
| --- | --- | --- |
| npm | `npm install --save-dev @dispat/bin` | `npm exec -- dispat status` |
| pnpm workspace | `pnpm add --workspace-root --save-dev @dispat/bin` | `pnpm exec dispat status` |
| Yarn modern | `yarn add --dev @dispat/bin` | `yarn dispat status` |

The Yarn examples below use its `node-modules` linker. [Yarn Classic](https://classic.yarnpkg.com/lang/en/docs/cli/add/) uses `yarn add --dev --ignore-workspace-root-check @dispat/bin` when adding the tool to a workspace root.

The package supports Node `^20.17.0 || >=22.9.0` and Linux, macOS, and Windows on x64 and ARM64. Installation needs HTTPS access to GitHub and its release-asset hosts. It downloads the pinned native release and verifies its size, SHA-256 digest, and reported version. The npm package and binary share a major/minor line but have independent patches: `dispat --version` reports the binary version, while `npm ls @dispat/bin` reports the npm distribution version.

### Installation scripts and repair

npm 12 requires script approval for registry installs. For a global install:

```sh
npm install --global @dispat/bin --allow-scripts=@dispat/bin
```

For local installs, retain the project's script-approval policy. If scripts were disabled or the native download failed, run the installer explicitly:

```sh
npm explore @dispat/bin -- node build/bin/postinstall.js
# For a global installation:
npm explore --global @dispat/bin -- node build/bin/postinstall.js
```

With pnpm, approve this package through [pnpm's script policy](https://pnpm.io/cli/approve-builds), or run `node node_modules/@dispat/bin/build/bin/postinstall.js` explicitly. That explicit path also works with Yarn's `node-modules` linker. Ordinary commands never download a missing binary.

npm proxy and certificate settings apply to the download. When invoking the installer directly, pass the corresponding `npm_config_https_proxy`, `npm_config_proxy`, `npm_config_noproxy`, or `npm_config_cafile` environment settings as needed. Set `DISPAT_NPM_DEBUG=1` for installer diagnostics on stderr. Native command diagnostics use `dispat --log-level debug` or `dispat --log-format json`.

## Start with a release plan

Run these commands from the Git repository root after installation. The examples use npm; substitute the runner from the installation table for pnpm or Yarn.

```sh
npm exec -- dispat init --format yaml
# Edit dispat.yaml: package paths, build commands, and publication commands.
npm exec -- dispat compute --write
npm exec -- dispat status
```

`init` creates a starter configuration. `compute --write` reads supported manifests, derives dependency edges, and records initial versions for existing packages. Review and commit those configuration changes. `status` shows what would release without changing files or contacting a publication destination.

For an existing project, preserve its published tag format and version baseline. Follow [the adoption guide](https://dispat.dev/examples/adopting/) before replacing its release workflow.

You can expose the preview through the root `package.json`:

```json
{
  "scripts": {
    "release:plan": "dispat status",
    "release:notes": "dispat preview"
  }
}
```

## An npm monorepo

Keep the root package private and declare its workspaces. Each releasable package has its own `package.json`, name, version, and build script:

```text
project/
  package.json
  package-lock.json
  dispat.yaml
  packages/
    core/package.json
    client/package.json
  apps/
    backend/package.json
    frontend/package.json
```

The root manifest includes:

```json
{
  "name": "example-workspace",
  "private": true,
  "workspaces": ["packages/*", "apps/*"]
}
```

Install workspace dependencies from the root. To publish the two libraries, use this `dispat.yaml`:

```yaml
scripts:
  build: npm run build
  publish: npm publish --access public
  npm-lock: cd "$(git rev-parse --show-toplevel)" && npm install --package-lock-only --ignore-scripts

spaces:
  libs:
    path: packages
    autoVersion:
      enabled: true
      syncLock: [npm-lock]
    flow:
      build: build
      publish: publish

dependencies:
  client: [core]

commit:
  enabled: true
  include: [package-lock.json]
```

Each direct child of `packages` becomes a dispat package. Scripts run in that package's directory, so `npm run build` calls its own build script. `autoVersion` writes the planned version and managed dependency ranges; the lock synchronization command runs at the repository root. The release commit includes the shared lockfile. Applications under `apps` are added separately in the deployment example below.

Use dispat package keys such as `core` and `client` in commit scopes, even when their npm names are `@example/core` and `@example/client`. A dependency defines ordering; reaching consumers with release intent is explicit:

```text
fix(core): handle empty input
feat(core)^: expose a new client API
```

The first record releases `core`. The second also reaches its direct consumers. Use `^^` for all transitive consumers. Package versions are independent by default; configure [version groups](https://dispat.dev/reference/releasing/versioning/) when packages must share a version or a major/minor line.

Local workspace builds can use provider build output. If a consumer must fetch the newly published provider from a registry, configure `isBuildWaitingPublish: true` on that provider. See [dependency ordering](https://dispat.dev/configuration/dependencies/) and [the npm example](https://dispat.dev/examples/npm/).

## A pnpm workspace

Use the same folder layout with a root `pnpm-workspace.yaml`:

```yaml
packages:
  - packages/*
  - apps/*
```

Install dispat at the root with `pnpm add -Dw @dispat/bin`. In the npm release configuration above, replace the script commands with:

```yaml
scripts:
  build: pnpm run build
  publish: pnpm publish --access public --no-git-checks
  pnpm-lock: cd "$(git rev-parse --show-toplevel)" && pnpm install --lockfile-only --ignore-scripts
```

Set `autoVersion.syncLock` to `[pnpm-lock]`, add `range: "workspace:*"` when your internal dependencies use that protocol, and replace `package-lock.json` with `pnpm-lock.yaml` in `commit.include`. pnpm converts workspace references when packing. `--no-git-checks` accommodates manifests rewritten during the release; dispat records completed publications with package tags.

CI installs dependencies with `pnpm install --frozen-lockfile` before release work. Keep the package's installation-script approval or explicit repair step in that setup. See [the pnpm guide](https://dispat.dev/examples/pnpm/) for the complete configuration.

## Yarn workspaces

Modern Yarn uses the root `workspaces` field shown in the npm example. For this package's filesystem-based installer, the setup below uses the standard `node_modules` layout in `.yarnrc.yml`:

```yaml
nodeLinker: node-modules
```

Add dispat at the root with `yarn add --dev @dispat/bin`, then use `yarn dispat status`. Keep your existing Yarn version pinned in the project's `packageManager` field. These instructions cover modern Yarn; Classic uses different installation and publishing commands.

Replace the release scripts with:

```yaml
scripts:
  build: yarn run build
  publish: yarn npm publish --access public
  yarn-lock: cd "$(git rev-parse --show-toplevel)" && YARN_ENABLE_IMMUTABLE_INSTALLS=false yarn install --mode=update-lockfile
```

Set `autoVersion.syncLock` to `[yarn-lock]` and put `yarn.lock` in `commit.include`. If internal ranges use `workspace:*`, set `autoVersion.range` to that literal. Use `yarn install --immutable` for the initial CI dependency install. The release's lock synchronization command permits the planned version changes to update the lockfile, including in CI where Yarn defaults to immutable installs.

If your Yarn policy disables dependency scripts, approve the package through that policy or run the explicit installer from the repair section. Plug'n'Play installation has not been verified for this distribution; an npm global install lets you keep an existing PnP project configuration while running `dispat` directly.

See Yarn's [installation modes](https://yarnpkg.com/features/linkers), [install options](https://yarnpkg.com/cli/install), and [npm publication command](https://yarnpkg.com/cli/npm/publish).

## Deploy a Node frontend and backend

Application deployment uses the same graph as library publication. Give each application a `build` and a `deploy` script appropriate to its host: the backend might build and push a container, while the frontend uploads static output or deploys a server application.

Add explicit application packages alongside the library space, with their own commands and flow:

```yaml
packages:
  backend:
    path: apps/backend
    scripts:
      app-build: npm run build
      app-deploy: npm run deploy
    flow:
      build: app-build
      publish: app-deploy
  frontend:
    path: apps/frontend
    dependencies: [backend]
    scripts:
      app-build: npm run build
      app-deploy: npm run deploy
    flow:
      build: app-build
      publish: app-deploy
```

The `publish` stage is the application's deployment command. Mark application manifests `private: true` when they should never be published to npm. The frontend's dependency orders its publication after the backend's. Add `isBuildWaitingPublish: true` to the backend if the frontend build itself needs the deployed backend to be available.

Use `DISPAT_NEW_VERSION` in deployment scripts to label an image or deployment. A consumer can read `DISPAT_WORKSPACE_BACKEND_VERSION` for the planned backend version. Your scripts should wait for the deployment's required readiness condition before reporting success. Supply host credentials through CI's runtime environment. See [script environment variables](https://dispat.dev/reference/environment/) and [release flows](https://dispat.dev/configuration/spaces/).

A single application uses the same pattern with just one entry under `packages`. For additional artifacts such as a container or documentation site, see [single-package releases](https://dispat.dev/examples/single-package/).

## Separate repositories and polyrepos

For independent releases, install dispat in each repository and keep that repository's configuration, release tags, and CI workflow there. Each invocation plans only the checkout it runs in. Dependencies between those independent release jobs remain part of your CI coordination.

When several repositories must release together, create a **control repository** with pinned Git submodules inside wrapper folders:

```text
release-control/
  dispat.yaml
  services/
    backend/
      src/                 # submodule: the backend repository
    frontend/
      src/                 # submodule: the frontend repository
```

The wrapper belongs to the control repository; its `src` folder belongs to the linked repository. This keeps changelogs and release configuration in the control repository. A deployment configuration can look like this:

```yaml
scripts:
  build: cd src && npm ci && npm run build
  deploy: cd src && npm run deploy

spaces:
  services:
    path: services
    autoVersion:
      enabled: false
    flow:
      build: build
      publish: deploy

dependencies:
  frontend: [backend]

commit:
  enabled: true
```

This example passes the planned version through the script environment and leaves linked manifests unchanged. If publishing a versioned library from a submodule, configure its manifest rewriting and publication explicitly; a wrapper's nested manifest needs different handling from a package-root manifest.

Clone the control repository with its full history and initialize pinned submodules with `git submodule update --init --recursive`. Move a pointer through a reviewed commit such as `feat(backend)^: deploy the new API`. That pointer-update commit supplies the release intent; dispat does not import the linked repository's commit history as the control repository's release history. The control repository owns the resulting release tags and records.

Read [the control-repository guide](https://dispat.dev/control-repository/) for setup, pointer synchronization, manifest handling, and recovery, or [one repository or many](https://dispat.dev/monorepo/) to compare the layouts.

## Run releases in CI

Install the project's pinned package manager and dependencies, make sure the native installer completed, and check out full Git history and release tags. Review `dispat status` on pull requests. The release workflow runs `dispat release` with the registry and deployment credentials required by its configured commands.

Keep publication commands specific to their destination. A stable npm package usually publishes with the `latest` dist-tag; a prerelease needs its intended channel tag. dispat exposes the release channel to scripts but does not add npm flags to your commands. The minimal `npm publish` examples above are for stable releases.

If a publication fails after an external upload succeeded, inspect that exact destination before retrying. dispat records completed package publications, while your publish script must reconcile its own partial writes. Configure how release commits and tags reach the remote, and keep registry authentication in CI rather than source configuration. See [dispat in CI](https://dispat.dev/reference/ci/) and [release records](https://dispat.dev/configuration/records/).

## Update or remove dispat

Update a local npm dependency with `npm update @dispat/bin`, or a global install with `npm install -g @dispat/bin@latest`. For pnpm or Yarn, update the root development dependency with that package manager and commit the updated lockfile.

To force installation or roll back, install the required npm package version explicitly, for example `npm install -g @dispat/bin@1.10.0 --force`. Retain any required installation-script approval when updating. The launcher rejects mutating `dispat self-update` commands; `self-update --check` and help remain available. Native update notifications are disabled for npm-managed execution.

Uninstall globally with `npm uninstall -g @dispat/bin`, or remove the local development dependency with your package manager.

## Documentation and community

- [Getting started](https://dispat.dev/getting-started/): installation, configuration, and your first release plan.
- [Commit messages](https://dispat.dev/reference/commits/): version intent, consumer propagation, and prereleases.
- [Configuration](https://dispat.dev/configuration/): packages, spaces, scripts, and release records.
- [Discord](https://discord.gg/83PwVSCCmk): questions, integration help, and projects using dispat.
- [GitHub issues](https://github.com/yohimik/dispat/issues): bugs and feature requests.
- [Building, testing, and publication recovery](https://github.com/yohimik/dispat/blob/main/packages/cli/TESTING.md): maintenance of this npm distribution.
