# Changelog

## packages/cli/v1.11.0-rc.0 (2026-09-15)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.11.0-rc.0): 1.10.0 -> 1.11.0-rc.0


## packages/cli/v1.10.3 (2026-09-13)

### Fixes

- record solo package changelog at repository root ([cc66610](https://github.com/yohimik/dispat/commit/cc666103070be3d51425a1a49825163a9ff4556a)) (by yohimik)
  Configure the src/lib package changelog as ../CHANGELOG.md and include it
  with root manifests in the release commit. Exercise creation, preserved
  history, tagged content, no-op releases and dirty-draft protection with
  the installed artifact, without changing Go behavior.

- document and verify single-root npm setup ([3f7174d](https://github.com/yohimik/dispat/commit/3f7174dc71fcb32bd5146ccc4d79bf729c0c3fd9)) (by yohimik)
  Keep one root package.json with @dispat/bin as a development dependency
  and a standalone src or lib package. Verify root version and lock updates,
  package contents, local release records, change ownership and failure guards
  with the installed artifact. Preserve existing Go behavior.

### Authors

- yohimik


## packages/cli/v1.10.2 (2026-09-11)

### Fixes

- repair blocked global installs ([8dc9f82](https://github.com/yohimik/dispat/commit/8dc9f82d7a11d3aa8078d809792c3ddfd30693fb)) (by yohimik, Codex (gpt-5.6-sol))
  Print exact, shell-safe repair commands and retain script approval on global
  updates. Cover blocked and approved packed installs with npm 12 gates. Add
  saga and orchestration keywords to the npm distribution.

### Authors

- yohimik
- Codex (gpt-5.6-sol)


## packages/cli/v1.10.1 (2026-09-11)

### Fixes

- improve npm metadata and standalone package setup ([79cb3d9](https://github.com/yohimik/dispat/commit/79cb3d9ec077b7a6179990c974434d4fd7621903)) (by yohimik, Codex (gpt-6-astra))
  Add discovery keywords, normalize the homepage URL, and document a single-package release configuration with version and lockfile updates.

### Authors

- yohimik
- Codex (gpt-6-astra)


## packages/cli/v1.10.0 (2026-09-11)

### Fixes

- publish the npm distribution as @dispat/bin ([40c8500](https://github.com/yohimik/dispat/commit/40c850094c95c5447827bd10213cb373a1f78d1c)) (by yohimik, Codex (gpt-6-astra))
  Publish @dispat/bin under the existing cli version group. Update package
  identity, installer guidance, artifact checks, and post-release installation.
  Wait up to five minutes for exact-version registry metadata after records.

  Release-As: 1.10.0

- publish dispat through npm ([f56f86e](https://github.com/yohimik/dispat/commit/f56f86e056e6cb5895b33618d3a1e37d29700cba)) (by yohimik)
  Install the pinned native dispat through npm. Keep artifact checks in the
  release build and let the publish stage execute only npm publish.

- distribute dispat through npm ([a2895ab](https://github.com/yohimik/dispat/commit/a2895ab364abbe93bb52fd7c0cbe57ef917b36bd)) (by yohimik)
  Install dispat globally, as a project dependency, or through npm exec.
  Download and verify the pinned native 1.10.0 binary for macOS, Linux,
  and Windows on x64 and ARM64. Manage upgrades and rollbacks through npm,
  with repair instructions for installations that disable lifecycle scripts.

- distribute the CLI through npm ([597e106](https://github.com/yohimik/dispat/commit/597e1064a2651f93132e678f4f4ed33c1d5bfc13)) (by yohimik)

### Authors

- yohimik
- Codex (gpt-6-astra)
