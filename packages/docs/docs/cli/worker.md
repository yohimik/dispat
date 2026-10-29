# `worker` migration

`dispat worker` was removed. Use [`dispat choreographer`](./choreographer.md) to serve tasks.
Replace `execution.workers` with `execution.choreographers`, `--worker` with `--choreographer`,
and the `worker` placement with `choreographer`. The removed spellings fail before task effects.

A script named `worker` remains available through `dispat run worker`. Keep the serving node's existing
state directory when upgrading so unresolved attempts and capacity reservations remain available for recovery.
