# Changelog

## docker/dispat-ubuntu/v1.11.0-rc.5 (2026-09-23)

### Fixes

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

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.11.0-rc.5): 1.11.0-rc.4 -> 1.11.0-rc.5

### Authors

- yohimik


## docker/dispat-ubuntu/v1.11.0-rc.4 (2026-09-23)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.11.0-rc.4): 1.11.0-rc.3 -> 1.11.0-rc.4


## docker/dispat-ubuntu/v1.11.0-rc.3 (2026-09-20)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.11.0-rc.3): 1.11.0-rc.0 -> 1.11.0-rc.3


## docker/dispat-ubuntu/v1.11.0-rc.0 (2026-09-15)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.11.0-rc.0): 1.10.0 -> 1.11.0-rc.0


## docker/dispat-ubuntu/v1.10.0 (2026-09-09)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.10.0): 1.9.0 -> 1.10.0


## docker/dispat-ubuntu/v1.9.0 (2026-09-09)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.9.0): 1.8.2 -> 1.9.0


## docker/dispat-ubuntu/v1.8.2 (2026-09-06)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.8.2): 1.8.1 -> 1.8.2


## docker/dispat-ubuntu/v1.8.1 (2026-09-06)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.8.1): 1.8.0 -> 1.8.1


## docker/dispat-ubuntu/v1.8.0 (2026-09-05)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.8.0): 1.7.2 -> 1.8.0


## docker/dispat-ubuntu/v1.7.2 (2026-09-03)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.7.2): 1.7.1 -> 1.7.2


## docker/dispat-ubuntu/v1.7.1 (2026-09-02)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.7.1): 1.7.0 -> 1.7.1


## docker/dispat-ubuntu/v1.7.0 (2026-09-02)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.7.0): 1.6.0 -> 1.7.0


## docker/dispat-ubuntu/v1.6.0 (2026-08-31)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.6.0): 1.5.0 -> 1.6.0


## docker/dispat-ubuntu/v1.5.0 (2026-08-31)

### Dependencies

- [dispat](https://github.com/yohimik/dispat/releases/tag/services/dispat/v1.5.0): 1.4.0 -> 1.5.0


## docker/dispat-ubuntu/v1.4.0 (2026-08-30)

### Dependencies

- dispat: 1.3.1 -> 1.4.0

## docker/dispat-ubuntu/v1.3.1 (2026-08-28)

### Dependencies

- dispat: 1.3.0 -> 1.3.1

## docker/dispat-ubuntu/v1.3.0 (2026-08-28)

### Dependencies

- dispat: 1.2.0 -> 1.3.0

## docker/dispat-ubuntu/v1.2.0 (2026-08-28)

### Fixes

- retry apt around the mirror pool rotation


### Dependencies

- dispat: 1.1.1 -> 1.2.0

## docker/dispat-ubuntu/v1.1.1 (2026-08-26)

### Dependencies

- dispat: 1.1.0 -> 1.1.1

## docker/dispat-ubuntu/v1.1.0 (2026-08-20)

### Dependencies

- dispat: 1.0.2 -> 1.1.0

## docker/dispat-ubuntu/v1.0.2 (2026-08-19)

### Dependencies

- dispat: 1.0.1 -> 1.0.2

## docker/dispat-ubuntu/v1.0.1 (2026-08-19)

### Dependencies

- dispat: 1.0.0 -> 1.0.1

## docker/dispat-ubuntu/v1.0.0 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces

- restore the 1.0 rc train


### Features

- the ubuntu image tracks the stable CLI

- finalize the workspace for 1.0.0

- container images with dispat preinstalled

- publish the docker hub readme


### Fixes

- exercise the release pipeline across every package

- nested dependencies update againx2

- nested dependencies update again

- nested dependencies update

- dependencies update

- the fetch base follows its version argument

- push through buildx, a multi-platform image has no local tag


### Dependencies

- dispat: 1.0.0-rc.19 -> 1.0.0

## docker/dispat-ubuntu/v1.0.0-rc.19 (2026-08-16)

### Features

- the ubuntu image tracks the stable CLI


### Dependencies

- dispat: 1.0.0-rc.18 -> 1.0.0-rc.19

## docker/dispat-ubuntu/v1.0.0-rc.18 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.17 -> 1.0.0-rc.18

## docker/dispat-ubuntu/v1.0.0-rc.17 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.16 -> 1.0.0-rc.17

## docker/dispat-ubuntu/v1.0.0-rc.16 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.15 -> 1.0.0-rc.16

## docker/dispat-ubuntu/v1.0.0-rc.15 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.14 -> 1.0.0-rc.15

## docker/dispat-ubuntu/v1.0.0-rc.14 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.13 -> 1.0.0-rc.14

## docker/dispat-ubuntu/v1.0.0-rc.13 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.12 -> 1.0.0-rc.13

## docker/dispat-ubuntu/v1.0.0-rc.12 (2026-08-16)

### Fixes

- exercise the release pipeline across every package


### Dependencies

- dispat: 1.0.0-rc.11 -> 1.0.0-rc.12

## docker/dispat-ubuntu/v1.0.0-rc.11 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.10 -> 1.0.0-rc.11

## docker/dispat-ubuntu/v1.0.0-rc.10 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.9 -> 1.0.0-rc.10

## docker/dispat-ubuntu/v1.0.0-rc.9 (2026-08-16)

### Fixes

- nested dependencies update againx2


### Dependencies

- dispat: 1.0.0-rc.8 -> 1.0.0-rc.9

## docker/dispat-ubuntu/v1.0.0-rc.8 (2026-08-16)

### Dependencies

- dispat: 1.0.0-rc.7 -> 1.0.0-rc.8

## docker/dispat-ubuntu/v1.0.0-rc.7 (2026-08-16)

### Dependencies

- ccme: 1.0.0-rc.4 -> 1.0.0-rc.5
- dispat: 1.0.0-rc.6 -> 1.0.0-rc.7

## docker/dispat-ubuntu/v1.0.0-rc.6 (2026-08-16)

### Fixes

- nested dependencies update again

- nested dependencies update


### Dependencies

- dispat: 1.0.0-rc.5 -> 1.0.0-rc.6

## docker/dispat-ubuntu/v1.0.0-rc.5 (2026-08-16)

### Fixes

- dependencies update


### Dependencies

- dispat: 1.0.0-rc.4 -> 1.0.0-rc.5

## docker/dispat-ubuntu/v1.0.0-rc.4 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces


### Features

- container images with dispat preinstalled


### Fixes

- the fetch base follows its version argument

- push through buildx, a multi-platform image has no local tag


### Dependencies

- dispat: 1.0.0-rc.3 -> 1.0.0-rc.4
