# Changelog

## specs/ccme-spec/v3.1.0-rc.1 (2026-09-20)

### Fixes

- verify the heading the specification has ([2068769](https://github.com/yohimik/dispat/commit/206876905db716a7e48415c28d640560e61b1300)) (by yohimik, Claude Fable 5.1)
  verify.sh required "### 27.11 Choreographed saga", which the specification
  renamed to "Linked peer topology", so the release failed at the beforeVersion
  hook after other packages had already published. test.sh only ever verified a
  hand-written stand-in for SPEC.md and could not notice; it now runs the
  verifier against the real document first.

- describe linked release ownership without saga selectors ([35664e1](https://github.com/yohimik/dispat/commit/35664e1685e703c347233421f9faefdba8b1622a)) (by yohimik)

- clarify fleet locking and partial release recovery ([2f591b0](https://github.com/yohimik/dispat/commit/2f591b0139339d13f20c4718056d14be6e0dea98)) (by yohimik)

### Authors

- yohimik
- Claude Fable 5.1


## specs/ccme-spec/v3.1.0-rc.0 (2026-09-15)

### Features

- define polyrepository Git planning ([7e38ae1](https://github.com/yohimik/dispat/commit/7e38ae131734159acb7011a835219f765c46a983)) (by yohimik)
  Add the optional profile for repository identities, fixed histories, consumer
  boundaries, source records, fleet locks and bounded planning indexes.

### Authors

- yohimik


## specs/ccme-spec/v3.0.2 (2026-09-09)

### Fixes

- clarify commit validation ([4c22611](https://github.com/yohimik/dispat/commit/4c22611dc02c6a88e39fae48f9b3184cdb3ae2fe)) (by yohimik)

### Authors

- yohimik


## specs/ccme-spec/v3.0.1 (2026-09-06)

No changes: a version set by Release-As.


## specs/ccme-spec/v3.0.0 (2026-09-06)

No changes: a version set by Release-As.


## specs/ccme-spec/v2.0.0 (2026-09-05)

### Breaking Changes

- separate the specification ([1c96acd](https://github.com/yohimik/dispat/commit/1c96acda11e7e5efefe32a7dcb410a841c76fc02)) (by yohimik)

### Fixes

- use native version replacement ([417fdd5](https://github.com/yohimik/dispat/commit/417fdd5467b279216fcdb8008c6de2a5def797bc)) (by yohimik)

- stamp before building ([6b29eb6](https://github.com/yohimik/dispat/commit/6b29eb6dde09adb4dcaa822f403ec7a912581706)) (by yohimik)

### Authors

- yohimik
