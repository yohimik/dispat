# Changelog

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

