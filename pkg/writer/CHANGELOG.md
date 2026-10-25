# Changelog

## pkg/writer/v1.2.2-rc.2 (2026-10-25)

### Fixes

- open a file for rewriting into one buffer of its size ([62ae266](https://github.com/yohimik/dispat/commit/62ae266fb7e188aec2060b5a964cc268b46c4d01)) (by yohimik, Claude Opus 5.5)
  Noticed while fixing Replace, not an audit finding: openSplicer, which
  every writer entry point reads through, used io.ReadAll, which starts at
  512 bytes and grows by copying, so a file cost about twice its size in
  allocations and two dozen buffers. It now reads into one buffer sized
  from the stat it already takes, with room to see the end of the file
  without growing. The size cap, the error paths and the bytes read are
  unchanged; a file that grew since the stat still reads whole up to the
  cap.

  TestAllocationBudget pins openSplicer of a 1 MiB file at 10
  allocations (9 measured); the old code takes 32.

  BenchmarkReplace, twenty expansions over a 1 MiB file, -benchtime 50x
  -count 3, medians, same machine (Apple M5, shared with other agents:
  allocations and bytes are the stable figures, timings are indicative;
  "applied" includes the atomic write and its fsync):

  | case      | B/op 38ec11ad | B/op this | allocs 38ec11ad | allocs this | time 38ec11ad | time this |
  |-----------|--------:|--------:|---:|---:|---------:|---------:|
  | applied   | 4356354 | 3176964 | 59 | 36 | 16176 us | 15918 us |
  | unchanged | 2241316 | 1061922 | 44 | 21 | 10463 us | 10853 us |

- answer Replace's probes without rescanning the file ([f6663ea](https://github.com/yohimik/dispat/commit/f6663ea338f501edae39c11f922f85bc8fdc1a31)) (by yohimik, Claude Opus 5.5)
  Finding: ReplaceBytes scans the file once per replacement, twice per
  rule expansion (pkg/writer/replace.go:124). The release Replacer hands
  Replace every replacement followed by its probe, a replacement whose
  Find is its Write. Replace only asks whether a probe occurs, so its own
  pass now stops at the first occurrence, and does not scan at all for a
  probe straight after the replacement that wrote its text, which is
  present by construction. ReplaceBytes keeps its exact counts. Both now
  replace in one Index pass sized by the count already taken, where
  bytes.ReplaceAll counted a second time, and write each replacement into
  the buffer the one before last produced instead of allocating a new
  file-sized buffer per replacement.

  The bytes, counts, Applied, Missing, Skipped and Count are those of the
  literal reading: FuzzReplaceMatchesTheLiteralReading holds both paths
  to it over replacement-and-probe lists, including a probe that does not
  follow its replacement and a replacement that removes a later Find
  (fuzzed one minute, 7.5M executions, no difference). The single-pass
  multi-pattern variant the audit sketched is not taken: as the
  equivalence vote shows, it changes which later Finds are counted.
  TestAllocationBudget pins ReplaceBytes over twenty applied
  replacements at 4 allocations (3 measured); the old code takes 21.

  BenchmarkReplace, twenty expansions over a 1 MiB file, -benchtime 50x
  -count 3, medians, same machine (Apple M5, shared with other agents:
  allocations and bytes are the stable figures, timings are indicative;
  "applied" includes the atomic write and its fsync):

  | case      | B/op 6248eff8 | B/op this | allocs 6248eff8 | allocs this | time 6248eff8 | time this |
  |-----------|---------:|--------:|---:|---:|---------:|---------:|
  | applied   | 23378592 | 4356354 | 79 | 59 | 38150 us | 16176 us |
  | unchanged |  2241636 | 2241316 | 45 | 44 | 14505 us | 10463 us |

### Dependencies

- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.2-rc.2): 1.2.2-rc.1 -> 1.2.2-rc.2

### Authors

- yohimik
- Claude Opus 5.5


## pkg/writer/v1.2.2-rc.1 (2026-09-25)

### Fixes

- describe how links treat every package.json override field ([cf5a1f7](https://github.com/yohimik/dispat/commit/cf5a1f7de84a60c92f51bb6bf1c5df5411159c19)) (by yohimik, Claude Opus 5.5)
  1.2.2-rc.0 changed how the writer reads and rewrites several manifests
  without a changelog line. In package.json, linking a name repoints every
  file: or link: copy of it across overrides, resolutions and
  pnpm.overrides, and removing it deletes every copy, while a registry
  override of the same name in another field stays as written; removing a
  name that only a registry override declares changes nothing and reports
  it in Missing. In Cargo.toml, a dependency version is found under a
  dotted key such as core.version = "1.0", under quoted or literal keys and
  table headers, and under a quoted key holding a literal dot, which stays
  apart from the dotted path it resembles. In every TOML manifest, text
  inside a multiline string is never read as a table header or a key, and
  an inline table rewrites only its own top-level member.

  The package README and the Go writer page describe the package.json
  behaviour, including a new link taking the place of a registry override
  in the chosen field, and a test holds the registry override cases.

### Dependencies

- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.2-rc.1): 1.2.2-rc.0 -> 1.2.2-rc.1

### Authors

- yohimik
- Claude Opus 5.5


## pkg/writer/v1.2.2-rc.0 (2026-09-23)

### Fixes

- preserve complete writes and reconcile release identities and late claims ([7fb8a84](https://github.com/yohimik/dispat/commit/7fb8a84693d4472e31a73d1cdc10f5db64578b88)) (by yohimik)

- isolate distributed outputs and preserve recovery state ([fe7c42f](https://github.com/yohimik/dispat/commit/fe7c42fa606781166be077e9322ff5e173295658)) (by yohimik)

- harden fleet release recovery and shared version policies ([dbf10c7](https://github.com/yohimik/dispat/commit/dbf10c73478d0bb9f298d4b1ec19cd6c8b177005)) (by yohimik)

### Dependencies

- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.2-rc.0): 1.2.1 -> 1.2.2-rc.0

### Authors

- yohimik


## pkg/writer/v1.2.1 (2026-09-20)

### Fixes

- restore the full-suite release gate ([85fb4e8](https://github.com/yohimik/dispat/commit/85fb4e8dae0c649398ff24633893767cbd58dd9b)) (by yohimik)
  Cover release recovery, locking, topology, configuration, manifest and installation edge cases. Reject nonregular manifest files before opening them, report unavailable Git during release startup, and correct the coverage gate assertion.

  Validated all 17 package test jobs, 1,315 integration tests with and without race detection, coverage freshness checks, and repository checks. Integration coverage: 23,826/25,073 statements (95.03%); combined coverage: 97.3%.

- preserve nonlocal overrides and reject malformed dependency edits ([2f591b0](https://github.com/yohimik/dispat/commit/2f591b0139339d13f20c4718056d14be6e0dea98)) (by yohimik)

### Dependencies

- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.1): 1.2.0 -> 1.2.1

### Authors

- yohimik


## pkg/writer/v1.2.0 (2026-09-05)

### Features

- support Aqua manifests ([18f3e2c](https://github.com/yohimik/dispat/commit/18f3e2c2891ad3959a02535f6b6d942a5c0fd326)) (by yohimik)

### Fixes

- reject symlink targets ([2219aa8](https://github.com/yohimik/dispat/commit/2219aa84cfc18b615a22e0351f9fcb0612feea2e)) (by yohimik)

### Dependencies

- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.0): 1.1.1 -> 1.2.0

### Authors

- yohimik


## pkg/writer/v1.1.1 (2026-08-19)

### Dependencies

- manifest: 1.1.0 -> 1.1.1

## pkg/writer/v1.1.0 (2026-08-19)

### Features

- unity, unreal, godot, o3de and defold manifests supported


### Dependencies

- manifest: 1.0.0 -> 1.1.0

## pkg/writer/v1.0.0 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces

- restore the 1.0 rc train


### Features

- manifest writing settles for the stable line

- finalize the workspace for 1.0.0

- rewrite manifest versions preserving every other byte

- write the build counters

- list and drop the local links

- export the Writer seam beside the scanner's

- read and write Dockerfiles and compose files

- add the zero-parsing Substitute API

- npm, yarn and pnpm overrides with file: paths

- replace a dependency with a local path

- cargo sub-table dependency form

- ruby, rust, c# and the nuget family

- iOS and Android manifests

- run consumers

- shared manifest module, package readmes

- compute auto version


### Fixes

- exercise depth-two propagation

- exercise a fixed-group member release

- exercise the release pipeline across every package

- nested dependencies update againx2

- nested dependencies update again again x2

- retract the versions the proxy pinned elsewhere

- nested dependencies update again

- nested dependencies update

- dependencies update

- carry the license in every module

- a pubspec version write keeps the build counter

- aim an npm link removal at the field that holds it

- command flags are not images, duplicate edits resolve together

- index toml tables once, single-pass verify

- report unwritable declarations as skipped

- pubspec nesting and msbuild property refs

- scanner and writer freeze


### Dependencies

- manifest: 1.0.0-rc.10 -> 1.0.0

## pkg/writer/v1.0.0-rc.10 (2026-08-16)

### Features

- manifest writing settles for the stable line


### Dependencies

- manifest: 1.0.0-rc.9 -> 1.0.0-rc.10

## pkg/writer/v1.0.0-rc.9 (2026-08-16)

### Fixes

- exercise depth-two propagation


### Dependencies

- manifest: 1.0.0-rc.8 -> 1.0.0-rc.9

## pkg/writer/v1.0.0-rc.8 (2026-08-16)

### Fixes

- exercise a fixed-group member release


### Dependencies

- manifest: 1.0.0-rc.7 -> 1.0.0-rc.8

## pkg/writer/v1.0.0-rc.7 (2026-08-16)

### Fixes

- exercise the release pipeline across every package


### Dependencies

- manifest: 1.0.0-rc.6 -> 1.0.0-rc.7

## pkg/writer/v1.0.0-rc.6 (2026-08-16)

### Fixes

- nested dependencies update againx2


### Dependencies

- manifest: 1.0.0-rc.5 -> 1.0.0-rc.6

## pkg/writer/v1.0.0-rc.5 (2026-08-16)

### Fixes

- nested dependencies update again again x2


### Dependencies

- manifest: 1.0.0-rc.4 -> 1.0.0-rc.5

## pkg/writer/v1.0.0-rc.4 (2026-08-16)

### Fixes

- retract the versions the proxy pinned elsewhere


### Dependencies

- manifest: 1.0.0-rc.3 -> 1.0.0-rc.4

## pkg/writer/v1.0.0-rc.3 (2026-08-16)

### Fixes

- nested dependencies update again

- nested dependencies update


### Dependencies

- manifest: 1.0.0-rc.2 -> 1.0.0-rc.3

## pkg/writer/v1.0.0-rc.2 (2026-08-16)

### Fixes

- dependencies update


### Dependencies

- manifest: 1.0.0-rc.1 -> 1.0.0-rc.2

## pkg/writer/v1.0.0-rc.1 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces


### Features

- rewrite manifest versions preserving every other byte


### Fixes

- carry the license in every module

- a pubspec version write keeps the build counter


### Dependencies

- manifest: 1.0.0-rc.0 -> 1.0.0-rc.1

## pkg/writer/v1.0.0-rc.0 (2026-08-09)

### Breaking Changes

- initial release

