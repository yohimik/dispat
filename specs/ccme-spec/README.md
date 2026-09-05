# CCME specification

This package contains the normative Conventional Commits: Monorepo Extension specification. `SPEC.md` defines the format and its conformance requirements. `LICENSE` carries the unchanged GPL-3.0 license text that applies to the specification.

The package has its own release line because changes to the specification and changes to Dispat's Go implementation are different compatibility decisions. Releases use tags such as `specs/ccme-spec/v2.0.0`. When no tag exists, Dispat uses the repository's `ccme-spec: 1.0.0` initial baseline. The 2.0.0 edition is a major revision because its corrected release algorithm changes plans for inputs that were valid under 1.0.0. The commit-message grammar is unchanged.

Run the package check from this directory:

```sh
sh verify.sh
```

The check requires one valid semantic version in `VERSION`, verifies that the normative version declarations in `SPEC.md` match it, and checks the local GPL license link and license text. It has no language runtime dependency.

`sh test.sh` runs the package's regression suite with Dispat from `PATH`, or an executable selected through `DISPAT_BIN`. The root release graph exposes it as the package's `tests` script, so changes under this folder are selected by the ordinary CI test sweep.

During a release, `version.sh` stages copies of `VERSION` and `SPEC.md`, uses `dispat replacer` to update their normative declarations, validates the staged package, and installs both files with rollback on an ordinary write failure. If restoration fails, it reports and preserves the original files in the staging directory. The two-file update is not atomic across a machine crash.

Version classification follows §17.3 of `SPEC.md`: editorial corrections that cannot change a plan are patches; forward-compatible additions are minor releases; any change to the plan for an already-valid input requires a major release.
