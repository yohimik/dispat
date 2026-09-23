# Changelog

## specs/agent-guide/v1.11.0-rc.5 (2026-09-23)

### Fixes

- retain authenticated coordination ownership through cancellation races ([7dceee9](https://github.com/yohimik/dispat/commit/7dceee974ac2937c6d5d64e3cec5c76df0822ed7)) (by yohimik)

- preserve uncertain publication and bound worker state ([e92ca16](https://github.com/yohimik/dispat/commit/e92ca16f0cbbee47a18a45b41f085f217e3e45da)) (by yohimik)

- bound transports and fence worker state ownership ([792522e](https://github.com/yohimik/dispat/commit/792522e7a8ad723724dd53fbb967d492d2ceaad9)) (by yohimik)

- harden fleet release recovery and shared version policies ([dbf10c7](https://github.com/yohimik/dispat/commit/dbf10c73478d0bb9f298d4b1ec19cd6c8b177005)) (by yohimik)

### Authors

- yohimik


## specs/agent-guide/v1.11.0-rc.4 (2026-09-23)

### Fixes

- keep the guide's tests on the machine with release authority ([768f2ed](https://github.com/yohimik/dispat/commit/768f2ed0ca62c05c4c867a7050fca3de59df89ef)) (by yohimik, Claude Fable 5.1)
  The guide's test suite performs releases on throwaway repositories, and a
  worker refuses release initiation by design, so a sweep that placed the
  suite on a worker failed it with E226. Every stage of the package now runs
  on the orchestrator.

- say that a sweep uses the pool and takes no lock ([4f65527](https://github.com/yohimik/dispat/commit/4f65527990454eaafa27841fda0ea120eecf1462)) (by yohimik, Claude Opus 5.5)
  An agent reading a distributed repository is told that `--worker` adds
  links for one invocation, and that `dispat run` places its tasks on the
  same pool, carries the declared `runOutputs` back, and takes no release
  lock, so it neither waits for a release nor stops one.

- describe a distributed release ([bede09b](https://github.com/yohimik/dispat/commit/bede09bd29412bdb946b214bc3ce411bfbc2be9d)) (by yohimik, Claude Opus 5)
  What to check before a run that delegates work, how to read the per-task
  summary, what an unknown publication outcome means, and why a retained lock
  is an operator's decision.

- say how a group's sharing rule limits a directive's reach ([e1b83ab](https://github.com/yohimik/dispat/commit/e1b83ab6e4235ae18e5992c2fb20f05345385c84)) (by yohimik)
  With independent channels only the packages a directive names enter or leave
  a train, so an agent reviewing version-group effects has to name every
  package it intends to move and confirm the result before releasing.

### Authors

- yohimik
- Claude Fable 5.1


## specs/agent-guide/v1.11.0-rc.3 (2026-09-20)

No changes: a version bump to keep the versioning group on one major and minor version.


## specs/agent-guide/v1.11.0-rc.2 (2026-09-20)

No changes: a version bump to keep the versioning group on one major and minor version.


## specs/agent-guide/v1.11.0-rc.1 (2026-09-20)

### Fixes

- document safe topology computation and graph repair ([35664e1](https://github.com/yohimik/dispat/commit/35664e1685e703c347233421f9faefdba8b1622a)) (by yohimik)

- clarify release intent and safe fleet recovery ([2f591b0](https://github.com/yohimik/dispat/commit/2f591b0139339d13f20c4718056d14be6e0dea98)) (by yohimik)

### Authors

- yohimik


## specs/agent-guide/v1.11.0-rc.0 (2026-09-15)

### Fixes

- clarify polyrepository configuration and release behavior ([88cf2ca](https://github.com/yohimik/dispat/commit/88cf2cae8b0355be0f5e185fc450f0c1949e4502)) (by yohimik, Codex (gpt-5.6-sol))
  Document owner-local folder inputs and parser diagnostics, direct channel
  precedence, and cancellation of hooks during durable release recording.

- document composed repository releases ([7e38ae1](https://github.com/yohimik/dispat/commit/7e38ae131734159acb7011a835219f765c46a983)) (by yohimik, Codex (gpt-5.6-sol))
  Describe central and imported configuration, external edges, source ownership,
  locks, exact pin handoff, diagnostics and partial publication recovery.

### Authors

- yohimik
- Codex (gpt-5.6-sol)


## specs/agent-guide/v1.10.3 (2026-09-11)

### Fixes

- define release intent and publication boundaries ([f56f86e](https://github.com/yohimik/dispat/commit/f56f86e056e6cb5895b33618d3a1e37d29700cba)) (by yohimik)
  Confirm the operator-approved package scope and inspect dispat status before
  releasing. Finish validation before publication and proceed directly to records.

### Authors

- yohimik


## specs/agent-guide/v1.10.2 (2026-09-10)

### Fixes

- clarify independent documentation patch versions ([9a48f92](https://github.com/yohimik/dispat/commit/9a48f929deb4ad0cf1601443822d7241c302dad9)) (by yohimik)
  Use the latest compatible documentation and guide patches without matching
  the CLI patch. Keep explanations of existing behavior on the current
  major/minor line and verify document package intent before releasing.

### Authors

- yohimik


## specs/agent-guide/v1.10.1 (2026-09-10)

### Fixes

- correct agent release interruption and integration guidance ([9411b5c](https://github.com/yohimik/dispat/commit/9411b5c4d6526c5d7423e0a658813925da7d679b)) (by yohimik)
  Interrupt invalid releases immediately when requirements change, inspect
  partial publication, and validate corrections before restarting.

  Summarize the important cross-project integration findings in six rows
  with references to the detailed ecosystem guidance.

- strengthen release integration checks ([fb422ca](https://github.com/yohimik/dispat/commit/fb422ca45b15247e03f32dec71dad27e33921ca6)) (by yohimik)
  Document native wrapper validation, minimum engine compatibility, host and
  plugin checks, immutable download inputs, and downstream patch evidence.
  Add a short integration checklist with references to the agent guide.

- guide dependent release propagation choices ([85294cc](https://github.com/yohimik/dispat/commit/85294cc4eb18232878b4018ba78fdb23545c6e76)) (by yohimik)

- clarify agent commit and release workflow guidance ([45fd18f](https://github.com/yohimik/dispat/commit/45fd18fea5d0dbdae5c922a4cb5117d720973c65)) (by yohimik)

### Authors

- yohimik


## specs/agent-guide/v1.10.0 (2026-09-09)

### Features

- diagnose messages ([1bb3b39](https://github.com/yohimik/dispat/commit/1bb3b397e4749461c1102869a56b1ceb600636ae)) (by yohimik)

### Authors

- yohimik


## specs/agent-guide/v1.9.0 (2026-09-09)

No changes: a version bump to keep the versioning group on one major and minor version.


## specs/agent-guide/v1.8.1 (2026-09-07)

### Fixes

- explain platform shell configs ([a767e93](https://github.com/yohimik/dispat/commit/a767e93205c45689b2f9fe6e6447f48ab6d7ba47)) (by yohimik)

### Authors

- yohimik


## specs/agent-guide/v1.8.0 (2026-09-06)

No changes: a version bump to keep the versioning group on one major and minor version.


## specs/agent-guide/v1.0.0 (2026-09-06)

No changes: a version set by Release-As.
