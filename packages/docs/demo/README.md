# Demo animations

The animated demos embedded in the repository README, on the documentation landing page, and on the commit-messages
page. Each one is an illustrated story, not a screen recording: package cards from web, service, game, and native workspaces (some
never joining a plan, because most of a monorepo is not in any given release), propagation lighting the edges,
manifests rewritten in place, and the CLI's own plan glyphs (`● changed`, `⊘ skipped`, `↻ catch-up`) telling it.
Every scene carries its own terminal at the bottom of the stage: the command that causes the next beat is typed
there, the pretty log's lines print at the moment the diagram shows the state they report, and every piece of text
is typed or printed the way a terminal would, never faded. Regenerating them takes one command:

```sh
brew install ffmpeg gifsicle
packages/docs/demo/render.sh
```

`render.sh` installs [`illustration/`](./illustration)'s dependencies on first run and renders the
[Remotion](https://www.remotion.dev) compositions used by the two committed GIFs in
[`imgs/`](../../../imgs). The landing page plays the React scenes directly with Remotion Player; it does not download
video exports. The source scenes are shared with the export tooling.

Every composition runs at twenty frames per second (`Root.tsx`). Terminal commands share a rate of 14 characters per second, with separate reading holds. Command length determines typing time. The
`Master` composition is the whole release story, forty-five seconds in five scenes; `Heal` is a cut of its timeline
(`SCENES` in [`src/Master.tsx`](./illustration/src/Master.tsx)), and the rest are their own storyboards. `Order` in
particular is not a cut: the master fails api on purpose, and the graph-not-a-list slide wants the run that
completes. The landing page's carousel pairs each CLI README key-feature bullet with its live scene (`FEATURE_MEDIA` in
[`DemoCarousel`](../src/components/DemoCarousel/index.tsx)), in the README's order, and follows them with the
`EXTRA_SLIDES` defined there. The asset column is a stable scene key; only the two GIF rows produce committed media:

| Composition | Asset | Story | Embedded in |
|---|---|---|---|
| `Master` | `demo-release.gif` | All five scenes in one take. | the repository [README](../../../README.md) |
| `Infra` | live scene | A versioned Terraform package rebuilds temporary state from known resources, saves and applies the plan, then the independent backend and frontend deploy after the apply. Git tags record completion without a separate progress database or persistent state bucket in this setup. | the carousel: "Terraform" |
| `Order` | `demo-order` | The run that completes: builds and publishes in dependency order, in parallel, the API Docker build waiting for core’s published Go module and the derived web image waiting for both the published API image and the built npm SDK assets, while that SDK builds from its local workspace during utils’ publish, and all five selected packages finishing. | the carousel: "Build and publish in dependency order" |
| `Blast` | `demo-blast`, `demo-blast.gif` | The same commit planned twice. As `feat(core)` only core releases; amended to `feat(core)^^` the whole consumer closure joins the plan, and utils, a provider, stays unchanged either way. | the carousel: "Blast radius written in the commit", and the [commit messages](../docs/reference/commits.md) page |
| `Heal` | `demo-heal` | api fails while independent core, utils, and sdk still ship. Repair the failing test, commit it, and rerun the same command to finish api and web. | the carousel: "Fix and rerun" |
| `Control` | `demo-control` | One package card answers a series of commits: a feat bumps it, `%beta` starts a prerelease train, a breaking change mid-train moves the whole train to the next major, `%beta>stable` graduates it there, `Release-As: none` holds it, `Release-As: auto` resumes it. | the carousel: "Release control from commits" |
| `Polyglot` | `demo-polyglot` | One manifest after another opens in the same editor and the version write happens in place, package.json to go.mod to Cargo.toml to pom.xml to pubspec.yaml to Info.plist to a Dockerfile, with the plist's build number pointedly untouched. | the carousel: "Polyglot by construction" |
| `Terminal` | `demo-terminal` | Three package rows, each with its own step set inside one run: core on the release's default order, api nesting `[changelog, commit]` before its publish, utils publishing its GitHub release from announce; then `dispat changelog` alone finds the work done (`W226`). | the carousel: "Every release step is also a command" |
| `Compute` | `demo-compute` | An npm workspace preview proposes one dependency and two initial versions with manifest evidence. A separate `--write` command applies the reviewed changes. | the carousel: "Discover dependencies from your package manifests" |
| `Run` | live scene | `dispat run tests --since HEAD~1 --consumers`: the checked fixture runs utils, then api and sdk, then the transitive web consumer; nothing releases, while core, docs, and mobile remain unselected. | the carousel: "Scripts for what changed" |
| `Single` | `demo-single` | The single-package example: one standalone entry, a scoped commit, the documentation's own status line, and a release leaving the tag, CHANGELOG.md, and a GitHub release under the card. | the carousel: "One package, no monorepo" |
| `Hooks` | `demo-hooks` | Three package rows across two spaces, the same stage strip in each, with only that package's configured hooks above it and the libs login visibly shared, while core's print-env hook writes the `DISPAT_*` environment into the terminal. | the carousel: "Stages, hooks, and one environment" |
| `Polyrepo` | `demo-polyrepo` | The control repository: three cards with git submodule pointers, a sync moving sdk's pointer, and the fleet releasing in dependency order while web stays unchanged. | the carousel: "Many repositories, one release" |
| `Why` | live scene | A concrete Dispat workflow: read the package graph, plan affected releases, publish providers before consumers, and use Git tags to plan unfinished work. | the carousel: "Build and publish one dependency graph" |
| `Aqua` | `demo-aqua` | The checked Aqua fixture is scanned through an imported package file, then its literal CLI version is updated while a dynamic private package is safely skipped. | the carousel: "Aqua manifests, read and rewritten directly" |
| `Math` | live scene | The same explicit inputs produce the same plan, without a persistent release cache, database, or clock-based version decisions. Recorded tags make completed versions safe to skip on reruns; ambiguous publishes still need reconciliation. The parser advances through each commit without backtracking. | the carousel: "Deterministic plans and recorded completion" |
| `Progress` | live scene | Confirmed tags record completed work. After an ambiguous publish response, the operator checks the destination or uses an idempotent publisher before retrying. | the carousel: "Recorded progress and safe retries" |
| `Glue` | `demo-glue` | Three acts: `dispat if` branching on `ENV=prod`, `dispat replacer` swapping a Gradle coordinate and a README install line, and the local-link bracket: `autowriter --link-local` writing the go.mod `replace`, tests against the tree, `--unlink-local`, and `scanner --verify-unlinked`. | the carousel: "The glue between the steps" |
| `For` | `demo-for` | A game engine change selects engine, game, and native packages. One command runs per package in dependency order through `DISPAT_ITEM`. | the carousel: "Run a command for each selected package" |
| `Lock` | live scene | Each run pushes a unique lock object with holder metadata; release removes it with an object-ID lease, so cleanup cannot delete another run's lock. A rejected second run plans and publishes nothing. | the carousel: "One release at a time" |

The scenes restate the documentation's claims:
[concepts](https://dispat.dev/concepts/),
[commit messages](https://dispat.dev/reference/commits/), and
[recovering from a failed run](https://dispat.dev/reference/releasing/recovery/). The palette and the
`#101713` background come from the documentation theme, the log captions use the pretty mode's colors, and the type
is JetBrains Mono throughout, so the animations read as the same product as the site and the terminal.

The live player reserves a fixed 1920×800 canvas beneath the page heading. It crops the unused top 280 pixels from
1920×1080 scenes; Aqua lays out its content directly in the shorter canvas. Playback and slide controls sit immediately
below that canvas, before the description, so changing slides or closing the transcript does not move the controls.
The transcript keeps the reader's open or closed choice across slide changes. Selecting a scene loads only that scene’s chunk; the current scene and its caption stay visible until the selection is ready. You can cancel a pending selection or choose another scene while it loads. A cancelled download cannot replace a newer choice. A failed load keeps the current scene available and offers a retry.

The player follows the site's light or dark theme through scoped `--demo-*` CSS variables. Standalone exports use the
dark fallback palette. Below 720px, shared scenes use a 720×1280 portrait layout with stacked cards and a wrapped terminal. The whole diagram fits the page without horizontal panning. Reduced motion starts on a still frame, and playback requires an explicit action.

The silent player starts muted and allocates no audio tags or audio context. The shared font loader requests only upright regular and bold Latin faces.

Run the browser checks against a locally served production build:

```sh
PLAYWRIGHT_RUNTIME=/path/to/node-runtime node packages/docs/demo/playwright-smoke.mjs \
  http://127.0.0.1:3000/ output/playwright/demos
```

`PLAYWRIGHT_RUNTIME` must contain a `node_modules/playwright` installation. The checks record desktop and mobile
playback in both themes and capture each slide. They also verify transcript state, control placement, keyboard
navigation, and reduced motion. `playwright-navigation.mjs` additionally checks delayed and failed scene loads, rapid selection, retries, and the shared sidebar category controls in both themes and current and historical docs. `playwright-anchors.mjs` verifies that page and version navigation clears stale heading anchors while preserving explicit links and browser history. Run both with the same arguments. `playwright-mobile.mjs` checks every portrait scene at three points in its timeline and its paused still. An optional fourth argument names one feature for a focused recheck. `playwright-menu.mjs` checks the mobile overlay height, internal scrolling, keyboard close/navigation, and reopening after browser back on landing, current docs, and 1.7 docs. `playwright-resources.mjs` checks silent autoplay, font requests, and audio allocation. `playwright-pages.mjs` checks shared page and code-block widths using `DISPAT_DOCS_URL` and `DISPAT_PAGES_OUTPUT`. `playwright-stories.mjs` records every complete story at 1× and checks terminal text for horizontal overflow throughout playback.

Validate command examples against a built CLI before changing their scenes:

```sh
DISPAT_DEMO_BIN=/absolute/path/to/dispat python3 packages/docs/demo/fixtures/verify.py
DISPAT_DEMO_BIN=/absolute/path/to/dispat python3 packages/docs/demo/fixtures/verify-release.py
DISPAT_DEMO_BIN=/absolute/path/to/dispat python3 packages/docs/demo/fixtures/verify-infra.py
DISPAT_DEMO_BIN=/absolute/path/to/dispat python3 packages/docs/demo/fixtures/verify-control.py
```

The verifiers use disposable Git repositories and check the displayed Compute, Run, For, and utility command results. Separate fixtures exercise scheduling, interrupted releases, infrastructure apply ordering, and every release-control directive. Animation timing remains separate from those command results.

The README gif is budgeted at 2.5 MiB and `render.sh` fails when a regeneration exceeds it. `pnpm studio` inside
`illustration/` opens the Remotion studio for editing the scenes.
