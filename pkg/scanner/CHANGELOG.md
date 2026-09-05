# Changelog

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

