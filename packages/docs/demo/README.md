# Demo animations

The animated demos embedded in the repository README, on the documentation landing page, and on the commit-messages
page. Each one is an illustrated story, not a screen recording: seven package cards across six ecosystems (some
never joining a plan, because most of a monorepo is not in any given release), propagation lighting the edges,
manifests rewritten in place, and the CLI's own plan glyphs (`● changed`, `⊘ skipped`, `↻ catch-up`) telling it.
Every scene carries its own terminal at the bottom of the stage: the command that causes the next beat is typed
there, the pretty log's lines print at the moment the diagram shows the state they report, and every piece of text
is typed or printed the way a terminal would, never faded. Regenerating them takes one command:

```sh
brew install ffmpeg gifsicle
packages/docs/demo/render.sh
```

`render.sh` installs [`illustration/`](./illustration)'s dependencies on first run, renders every
[Remotion](https://www.remotion.dev) composition to video, and converts them into the committed assets in
[`imgs/`](../../../imgs): two gifs, and a webm and mp4 pair per key feature that the landing page's carousel plays,
served at `/dispat/<name>` because `imgs/` is a static directory of the documentation site.

Every composition runs at twenty frames per second (`Root.tsx`), which is what sets the deck's unhurried pace. The
`Master` composition is the whole release story, forty-five seconds in five scenes; `Heal` is a cut of its timeline
(`SCENES` in [`src/Master.tsx`](./illustration/src/Master.tsx)), and the rest are their own storyboards. `Order` in
particular is not a cut: the master fails api on purpose, and the graph-not-a-list slide wants the run that
completes. The landing page's carousel pairs each CLI README key-feature bullet with its clip (`FEATURE_MEDIA` in
[`DemoCarousel`](../src/components/DemoCarousel/index.tsx)), in the README's order, and follows them with the
`EXTRA_SLIDES` defined there: six more stories the documentation describes, each with its own animation:

| Composition | Asset | Story | Embedded in |
|---|---|---|---|
| `Master` | `demo-release.gif` | All five scenes in one take. | the repository [README](../../../README.md) |
| `Order` | `demo-order` | The run that completes: builds and publishes in dependency order, in parallel, api waiting for core's publish before its Dockerfile rewrite, web following, all four published. | the carousel: "Releases the graph, not a list" |
| `Blast` | `demo-blast`, `demo-blast.gif` | The same commit planned twice. As `feat(core)` only core releases; amended to `feat(core)^^` the whole consumer closure joins the plan, and utils, a provider, stays unchanged either way. | the carousel: "Blast radius written in the commit", and the [commit messages](../docs/reference/commits.md) page |
| `Heal` | `demo-heal` | api's failure leaves web skipped while core and utils still ship, and the re-run finishes exactly what the first run still owed. | the carousel: "Self-healing runs" |
| `Control` | `demo-control` | One package card answers a series of commits: a feat bumps it, `%beta` starts a prerelease train, a breaking change mid-train moves the whole train to the next major, `%beta>stable` graduates it there, `Release-As: none` holds it, `Release-As: auto` resumes it. | the carousel: "Release control from commits" |
| `Polyglot` | `demo-polyglot` | One manifest after another opens in the same editor and the version write happens in place, package.json to go.mod to Cargo.toml to pom.xml to pubspec.yaml to Info.plist to a Dockerfile, with the plist's build number pointedly untouched. | the carousel: "Polyglot by construction" |
| `Terminal` | `demo-terminal` | Three package rows, each with its own step set inside one run: core on the release's default order, api nesting `[changelog, commit]` before its publish, utils publishing its GitHub release from announce; then `dispat changelog` alone finds the work done (`W226`). | the carousel: "Every release step is also a command" |
| `Compute` | `demo-compute` | The config's spaces, then `dispat compute` proposing four edges and a starting version with manifest evidence, each edge drawing as its line prints, confirmed with `--interactive` and applied with `--write`. | the carousel: "The graph comes from the manifests" |
| `Run` | `demo-run` | `dispat run tests --since HEAD~1 --consumers`: a utils fix runs the tests script on utils and its consumers api and sdk, in graph order; nothing releases and the rest of the graph, web included, is not selected. | the carousel: "Scripts for what changed" |
| `Single` | `demo-single` | The single-package example: one standalone entry, a scoped commit, the documentation's own status line, and a release leaving the tag, CHANGELOG.md, and a GitHub release under the card. | the carousel: "One package, no monorepo" |
| `Hooks` | `demo-hooks` | Three package rows across two spaces, the same stage strip in each, with only that package's configured hooks above it and the libs login visibly shared, while core's print-env hook writes the `DISPAT_*` environment into the terminal. | the carousel: "Stages, hooks, and one environment" |
| `Polyrepo` | `demo-polyrepo` | The control repository: three cards with git submodule pointers, a sync moving sdk's pointer, and the fleet releasing in dependency order while web stays unchanged. | the carousel: "Many repositories, one release" |
| `Why` | `demo-why` | The README's two breaking situations, drawn: a Docker consumer failing to build under build-all-then-publish-all, a mid-run error leaving half published with the rest unknown, and dispat running build and publish as legs of one graph. | the carousel: "Why one more monorepo tool?" |
| `Math` | `demo-math` | Three properties as three equations: `plan = f(history, graph, config)` with the identical status printed twice, `release(release(S)) = release(S)` with a failed run converging on re-run, and the parser's `O(n)` time in `O(1)` space as a cursor sweeping a commit once. | the carousel: "Mathematics, not machinery" |
| `Glue` | `demo-glue` | Three acts: `dispat if` branching on `ENV=prod`, `dispat replacer` swapping a Gradle coordinate and a README install line, and the local-link bracket: `autowriter --link-local` writing the go.mod `replace`, tests against the tree, `--unlink-local`, and `scanner --verify-unlinked`. | the carousel: "The glue between the steps" |
| `Lock` | `demo-lock` | The release lock: a runner claims the `dispat-release-lock` tag and releases, a second run is rejected with nothing planned or built, and the returned lock lets the retry claim cleanly. | the carousel: "One release at a time" |

The scenes restate the documentation's claims:
[concepts](https://yohimik.github.io/dispat/concepts/),
[commit messages](https://yohimik.github.io/dispat/reference/commits/), and
[recovering from a failed run](https://yohimik.github.io/dispat/reference/releasing/recovery/). The palette and the
`#101713` background come from the documentation theme, the log captions use the pretty mode's colors, and the type
is JetBrains Mono throughout, so the animations read as the same product as the site and the terminal.

The README gif is budgeted at 2.5 MiB and `render.sh` fails when a regeneration exceeds it. `pnpm studio` inside
`illustration/` opens the Remotion studio for editing the scenes.
