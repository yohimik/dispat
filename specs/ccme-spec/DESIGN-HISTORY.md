# CCME design history

## 2026-09-06: CCME 3.0.0 design disclosure

The project author, Semen Fediukovich (yohimik), proposed publishing two specification additions ahead of their implementation:

- Git remains the default version-control backend. Shareable trusted shell commands can adapt other backends through defined request/response formats while preserving complete history, deterministic ordering, immutable release records and ownership-safe locks.
- An explicit `rollback(scope)` commit directive requests withdrawal of an identified published version. A package or space provides the rollback handler. Missing handlers are preflight errors; completion is recorded separately without deleting release history or reusing versions.

The normative contracts are [VCS-PROTOCOL.md](./VCS-PROTOCOL.md) and [ROLLBACK.md](./ROLLBACK.md). The versioned release tag and Git history identify the exact disclosed text. This dated entry records the design and authorship statement; it is not a claim that Dispat implements either feature, that the design has been experimentally validated, or that publication grants exclusive rights.

The parser remains on its existing implementation line under an explicit release hold. The current runtime and its measurements must continue to cite the specification/behavior they actually implement. Future implementation and experimental validation should be recorded as separate dated entries.
