# Changelog

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
