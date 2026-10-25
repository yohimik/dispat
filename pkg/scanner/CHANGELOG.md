# Changelog

## pkg/scanner/v1.2.2-rc.2 (2026-10-25)

### Fixes

- read a manifest into one buffer of its size ([1e699b4](https://github.com/yohimik/dispat/commit/1e699b49b28c272640085b0b84fee1e0678623b9)) (by yohimik, Claude Opus 5.5)
  Noticed while fixing the line-scanned parsers, not an audit finding:
  readManifest, the read every scan makes for every manifest, used
  io.ReadAll, which starts at 512 bytes and grows by copying, so a file
  cost about twice its size in allocations and two dozen buffers. It now
  reads into one buffer sized from the stat it already takes, with room
  to see the end of the file without growing. The size cap, the error
  paths and the bytes read are unchanged; a file that grew since the
  stat still reads whole up to the cap.

  TestAllocationBudget pins readManifest of a 1 MiB file at 9
  allocations (8 measured); the old code takes 31.

  BenchmarkReadManifest, -benchtime 2000x -count 3, medians, same machine
  (Apple M5, shared with other agents: allocations and bytes are the
  stable figures, timings are indicative):

  | file    | B/op 56880a9c | B/op this | allocs 56880a9c | allocs this | time 56880a9c | time this |
  |---------|--------:|--------:|---:|---:|-------:|------:|
  | 4 KiB   |   11910 |    5638 | 16 |  8 |  14 us | 11 us |
  | 1 MiB   | 2238052 | 1058654 | 38 | 14 | 205 us | 86 us |

- clone the values line-scanned parsers keep ([aafdc8f](https://github.com/yohimik/dispat/commit/aafdc8ff50ef03cea8715c38066bde2cf205365d)) (by yohimik, Claude Opus 5.5)
  Finding: line-scanned parsers pin the whole file behind a few retained
  substrings (pkg/scanner/xcodeproj.go:59). The Xcode project, Gradle
  build, Dockerfile, Gemfile, gemspec, podspec, Podfile and requirements
  parsers cut their names, versions, ranges and paths out of one string
  copy of the file, so each manifest kept its whole file alive for the
  run. Each kept value is now cloned where the parser stores it, as the
  Unity and INI parsers already did. A Ruby declaration's requirements
  are joined into a builder, which copies, instead of a slice and Join,
  which returns a single requirement uncopied. The line loops that only
  read forward iterate strings.SplitSeq instead of materialising every
  line. The values, their order and the dependency lists are unchanged.

  TestLineScannedParsersRetainOnlyTheirValues fails on the old code: a
  4 MiB file kept about 4.16 MB live for every parser, against at most
  64 KiB allowed.

  BenchmarkLineScannedParsers, 1 MiB file, -benchtime 50x -count 3,
  medians, same machine (Apple M5, shared with other agents: retained-B
  and allocations are the stable figures, timings are indicative;
  retained-B varies by about 2 KB between runs):

  | parser       | retained-B a367d996 | retained-B this | B/op a367d996 | B/op this | allocs a367d996 | allocs this | time a367d996 | time this |
  |--------------|---------:|------:|---------:|---------:|------:|------:|---------:|---------:|
  | xcodeproj    |  1058680 |  1704 |  1286520 |  1057133 |     4 |     6 |  1574 us |   607 us |
  | gradle       |  1057040 |   256 |  3690997 |  3396118 | 36560 | 36565 |  2326 us |  1200 us |
  | dockerfile   |  1057696 |   296 | 19678577 | 19678413 | 94632 | 94633 | 10028 us |  7808 us |
  | gemfile      |  1056896 |  1832 |  1360366 |  1057258 |     8 |    11 |   614 us |   269 us |
  | gemspec      |  1057168 |  1768 |  1360484 |  1057372 |    25 |    28 |   847 us |   432 us |
  | podspec      |  1056944 |  1768 |  1360331 |  1057217 |    17 |    20 |   662 us |   288 us |
  | podfile      |  1057248 |  1832 |  1360391 |  1057272 |     9 |    12 |   656 us |   254 us |
  | requirements |  1057136 |   160 |  1360297 |  1360292 |    11 |    13 |   592 us |   359 us |

  The two or three extra allocations per file are the clones themselves,
  a few bytes each.

### Dependencies

- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.2-rc.2): 1.2.2-rc.1 -> 1.2.2-rc.2

### Authors

- yohimik
- Claude Opus 5.5


## pkg/scanner/v1.2.2-rc.1 (2026-09-25)

### Dependencies

- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.2-rc.1): 1.2.2-rc.0 -> 1.2.2-rc.1


## pkg/scanner/v1.2.2-rc.0 (2026-09-23)

### Dependencies

- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.2-rc.0): 1.2.1 -> 1.2.2-rc.0


## pkg/scanner/v1.2.1 (2026-09-20)

### Fixes

- restore the full-suite release gate ([85fb4e8](https://github.com/yohimik/dispat/commit/85fb4e8dae0c649398ff24633893767cbd58dd9b)) (by yohimik)
  Cover release recovery, locking, topology, configuration, manifest and installation edge cases. Reject nonregular manifest files before opening them, report unavailable Git during release startup, and correct the coverage gate assertion.

  Validated all 17 package test jobs, 1,315 integration tests with and without race detection, coverage freshness checks, and repository checks. Integration coverage: 23,826/25,073 statements (95.03%); combined coverage: 97.3%.

- preserve compatibility with the revised scanner API ([2f591b0](https://github.com/yohimik/dispat/commit/2f591b0139339d13f20c4718056d14be6e0dea98)) (by yohimik)

### Dependencies

- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.1): 1.2.0 -> 1.2.1

### Authors

- yohimik


## pkg/scanner/v1.2.0 (2026-09-05)

### Features

- support Aqua manifests ([18f3e2c](https://github.com/yohimik/dispat/commit/18f3e2c2891ad3959a02535f6b6d942a5c0fd326)) (by yohimik)

### Fixes

- prefer writable Aqua sources ([73a4e86](https://github.com/yohimik/dispat/commit/73a4e86bea17bf2e181628e9c54f6b4bc47ff08d)) (by yohimik)

### Dependencies

- [manifest](https://github.com/yohimik/dispat/releases/tag/pkg/manifest/v1.2.0): 1.1.1 -> 1.2.0

### Authors

- yohimik


## pkg/scanner/v1.1.1 (2026-08-19)

### Fixes

- unreal and unity version write


### Dependencies

- manifest: 1.1.0 -> 1.1.1

## pkg/scanner/v1.1.0 (2026-08-19)

### Features

- unity, unreal, godot, o3de and defold manifests supported


### Dependencies

- manifest: 1.0.0 -> 1.1.0

## pkg/scanner/v1.0.0 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces

- restore the 1.0 rc train


### Features

- workspace scanning settles for the stable line

- finalize the workspace for 1.0.0

- read dependency manifests into one shape

- report dropped declarations

- read and write Dockerfiles and compose files

- declare manifest names per package

- ruby, rust, c# and the nuget family

- iOS and Android manifests

- run consumers

- shared manifest module, package readmes

- compute auto version


### Fixes

- exercise the release pipeline across every package

- nested dependencies update againx2

- retract the versions the proxy pinned elsewhere

- nested dependencies update again

- nested dependencies update

- dependencies update

- carry the license in every module

- keep the partial-result contract and bound the read

- step over an unreadable folder instead of ending the scan

- scanner and writer freeze

- autoversion and compute correctness

- round 2 blockers


### Dependencies

- manifest: 1.0.0-rc.10 -> 1.0.0

## pkg/scanner/v1.0.0-rc.10 (2026-08-16)

### Features

- workspace scanning settles for the stable line


### Dependencies

- manifest: 1.0.0-rc.9 -> 1.0.0-rc.10

## pkg/scanner/v1.0.0-rc.9 (2026-08-16)

### Dependencies

- manifest: 1.0.0-rc.8 -> 1.0.0-rc.9

## pkg/scanner/v1.0.0-rc.8 (2026-08-16)

### Dependencies

- manifest: 1.0.0-rc.7 -> 1.0.0-rc.8

## pkg/scanner/v1.0.0-rc.7 (2026-08-16)

### Fixes

- exercise the release pipeline across every package


### Dependencies

- manifest: 1.0.0-rc.6 -> 1.0.0-rc.7

## pkg/scanner/v1.0.0-rc.6 (2026-08-16)

### Fixes

- nested dependencies update againx2


### Dependencies

- manifest: 1.0.0-rc.5 -> 1.0.0-rc.6

## pkg/scanner/v1.0.0-rc.5 (2026-08-16)

### Dependencies

- manifest: 1.0.0-rc.4 -> 1.0.0-rc.5

## pkg/scanner/v1.0.0-rc.4 (2026-08-16)

### Fixes

- retract the versions the proxy pinned elsewhere


### Dependencies

- manifest: 1.0.0-rc.3 -> 1.0.0-rc.4

## pkg/scanner/v1.0.0-rc.3 (2026-08-16)

### Fixes

- nested dependencies update again

- nested dependencies update


### Dependencies

- manifest: 1.0.0-rc.2 -> 1.0.0-rc.3

## pkg/scanner/v1.0.0-rc.2 (2026-08-16)

### Fixes

- dependencies update


### Dependencies

- manifest: 1.0.0-rc.1 -> 1.0.0-rc.2

## pkg/scanner/v1.0.0-rc.1 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces


### Features

- read dependency manifests into one shape


### Fixes

- carry the license in every module


### Dependencies

- manifest: 1.0.0-rc.0 -> 1.0.0-rc.1

## pkg/scanner/v1.0.0-rc.0 (2026-08-09)

### Breaking Changes

- initial release

