# Changelog

## packages/cli/v1.11.0-rc.5 (2026-09-25)

### Fixes

- authenticate the release packager's GitHub lookup ([b14497f](https://github.com/yohimik/dispat/commit/b14497f433d20e7adf60a79b8d877300e70ee99f)) (by yohimik, Claude Opus 5.5)
  The npm release build reads the exact dispat release it wraps from the GitHub
  API. That lookup was anonymous, so on a shared runner address it drew on the
  same 60-requests-an-hour quota as every other anonymous caller there:
  1.11.0-rc.5 failed with a 403 once the image builds had spent it, and
  make-fetch-happen retries 408, 420, 429 and server errors but never a 403.
  The packager now sends the release job's GITHUB_TOKEN, only to api.github.com
  over HTTPS. A rate-limited 403 or 429 waits for the time the response names
  (retry-after, or the rate limit's reset), at most a minute, for at most three
  attempts, and a refusal reports GitHub's own message.

  The npm-released, npm-version and npm-binary-version step outputs come from a
  postPublish hook named released, like the dispat package's, instead of an
  announce stage that announced nothing.

- reset TinyGo rollback retention and expose acceptance failures ([ad62412](https://github.com/yohimik/dispat/commit/ad62412ff79631548fd5a617a0c31d8854ab0790)) (by yohimik)

- bind every receipt and acknowledge cancellation that wins the result lease ([612a58b](https://github.com/yohimik/dispat/commit/612a58b5295015a13203cae8f4648f551a460913)) (by yohimik)

- authenticate queued transitions and settle concurrent withdrawal safely ([f694114](https://github.com/yohimik/dispat/commit/f6941140a99209e851ecf889c54b9aa8cfd023e4)) (by yohimik)

- retain authenticated coordination ownership through cancellation races ([7dceee9](https://github.com/yohimik/dispat/commit/7dceee974ac2937c6d5d64e3cec5c76df0822ed7)) (by yohimik)

- preserve complete writes and reconcile release identities and late claims ([7fb8a84](https://github.com/yohimik/dispat/commit/7fb8a84693d4472e31a73d1cdc10f5db64578b88)) (by yohimik)

- settle worker cleanup races and report failed output merges ([96c435e](https://github.com/yohimik/dispat/commit/96c435e24f2de9a181bbf13b4c2bf49036d29cc0)) (by yohimik)

- isolate distributed outputs and preserve recovery state ([fe7c42f](https://github.com/yohimik/dispat/commit/fe7c42fa606781166be077e9322ff5e173295658)) (by yohimik)

- admit durable receipts and retain fleet command ownership ([f2f1d96](https://github.com/yohimik/dispat/commit/f2f1d965679a22cfe00adb8170e37ee9bd60bdb7)) (by yohimik)

- keep Git maintenance attached and stream tag receipts ([dd79291](https://github.com/yohimik/dispat/commit/dd7929143c99a16f3d920fe99e220d79be8331a0)) (by yohimik)

- preserve uncertain publication and bound worker state ([e92ca16](https://github.com/yohimik/dispat/commit/e92ca16f0cbbee47a18a45b41f085f217e3e45da)) (by yohimik)

- bound transports and fence worker state ownership ([792522e](https://github.com/yohimik/dispat/commit/792522e7a8ad723724dd53fbb967d492d2ceaad9)) (by yohimik)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.11.0-rc.6): 1.11.0-rc.5 -> 1.11.0-rc.6

### Authors

- yohimik
- Claude Opus 5.5


## packages/cli/v1.11.0-rc.4 (2026-09-23)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.11.0-rc.4): 1.11.0-rc.3 -> 1.11.0-rc.4


## packages/cli/v1.11.0-rc.3 (2026-09-20)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.11.0-rc.3): 1.11.0-rc.0 -> 1.11.0-rc.3


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
