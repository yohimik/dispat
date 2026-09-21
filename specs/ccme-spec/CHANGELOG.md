# Changelog

## specs/ccme-spec/v3.1.0-rc.2 (2026-09-21)

### Features

- define distributed execution and verified output reuse ([80b4d8c](https://github.com/yohimik/dispat/commit/80b4d8c101ce4437ce762284e85f7f2a5125997a)) (by yohimik)
  Specify orchestrator and worker authority, complete locking before planning, task transport, output admission, publication fencing and native recovery. Integrate the optional profile across all three history modes and clarify existing planning bounds and conformance cases. Distributed execution remains an unimplemented specification contract.

- define shared-version groups and their sharing axes ([da64d0f](https://github.com/yohimik/dispat/commit/da64d0f05422d3ba647a22e1f1b3a32b3fff4a10)) (by yohimik, Claude Opus 5)
  The specification used "version group" and "shared-version group" in §13.1,
  §13.11 and §27 without ever defining one, and said nothing about a group
  sitting on a prerelease train, which is where every question a group raises
  becomes hard: whose counter is it, whose channel is it, and what does a retry
  owe.

  §13.9a answers them, normative for an engine that offers groups and
  explicitly optional otherwise. It defines membership, the group baseline and
  line, the three sharing axes and the one combination that has no meaning, the
  engagement table, the member target floor that lets a rider compute a version
  its own window does not explain, the rule that a channel proposal is a
  directive rather than a resting position, assignment under each channel axis,
  and alignment. It states the guarantees honestly: §13.7c holds per member
  under an independent counter, and under a shared one a retry advances the
  counter, so G3 there covers the core.

  §2 gains the two terms, §11.4, §11.5, §13.1 and §13.9 point at the floor,
  §14 lists the key, §15.8 collects the edge cases and Appendix B.12 carries
  the vectors, matching the engine's own tests one to one.

### Fixes

- state where a withheld floor is made up for ([b2b5b14](https://github.com/yohimik/dispat/commit/b2b5b14fceb8f553c6099106c0d294da8a30e3d7)) (by yohimik)
  §13.9a's alignment rule said a releasing laggard under an independent counter
  is raised by the floor, which is not the whole answer: the member the floor
  declines to lift is brought to the prefix by alignment. §15.8 and B.12 gain
  the case.

- correct the hand-edited-tag vector of B.12 ([bdf7e47](https://github.com/yohimik/dispat/commit/bdf7e47e239601e1d0ed4aff06640bdebabb7d1d)) (by yohimik)
  Vector 143 put the hand-edited tag on the member that was also the group's
  own baseline, which is not the case it meant to describe: the guard then
  fires against the group's computation and, being repository-scoped, aborts
  the run before any member is assigned. The vector now states a member below
  the group's baseline and above its line, which is what produces the E185 it
  claims, and names the other outcome rather than leaving it to be discovered.

- describe three history modes without a publishing repository ([c9b34f1](https://github.com/yohimik/dispat/commit/c9b34f1335dc0a78efdb1f8f4ecbdd090120aea4)) (by yohimik, Claude Fable 5.1)
  §27 now opens with the three modes an engine runs in: a single history,
  specified histories, and histories discovered through a topology. The
  control repository is only the name of the repository holding the entry
  configuration of the second mode. It is not where releases come from:
  packages publish from their own paths, a checkpoint is optional evidence,
  a source may release on its own in the first mode, and the profile does not
  fix where the engine is invoked. Vector 28 of §27.10 pins the standalone
  source release.

  Wording that implied otherwise is gone: the checkpoint "after each source
  release", the "control run", the "central hub topology", and the last three
  "saga" phrasings of §27.11.

  No release plan changes.

- admit settlement heads and tighten fleet cost bounds ([a7b35ab](https://github.com/yohimik/dispat/commit/a7b35abcbbabca01fef68fbc22418f9099460f64)) (by yohimik, Claude Fable 5.1)
  A settlement moves heads before the revalidation point of §27.2, the
  consumer's own included, and §27.2 admitted only a native record step, so a
  literal reading refused every settled consumer with E330. The exact
  revision a settlement wrote is now an admitted transition, which is what the
  engine already does; vector 28 of §27.12 and the docs page say so.

  The cost model is corrected where it was loose or silent. The publication
  input closure is one condensation and one bitset union per edge, not one
  traversal per repository. A fresh window is the meet of two single-boundary
  windows, so reachability classes count distinct boundary commits and never
  (stable, fresh) pairs, and a window is keyed by the repository it ranges over
  and not by the owner of the package reading it (vector 29). The linked peer
  topology gains the rows it never had: link evidence and settlement, with a
  star bounding every settlement at two commits.

  No release plan changes.

### Authors

- yohimik
- Claude Opus 5


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
