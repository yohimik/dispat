# The TinyGo spike

Read this page to understand why dispat's release binaries are built with the Go compiler and what would have to change
for that to be worth revisiting. It describes a spike: an experiment kept in the repository because its answer is a
version number away from changing, not a gate any job runs.

TinyGo produces much smaller binaries than gc for the same source. A CLI distributed as six platform binaries has an
obvious interest in that, so the question was asked properly, with the whole answer written down.

## What the spike is

[`Dockerfile.tinygo`](https://github.com/yohimik/dispat/blob/main/Dockerfile.tinygo) at the repository root, run by the
`tinygo-spike` script in the root `dispat.yaml`:

```sh
dispat exec tinygo-spike
```

It is a chain of build targets, one question each, and no target aborts. A spike exists for its log, and a build that
dies on its first failure reports one fact where the matrix needs all of them, so every step records its own exit
status and the export stage collects the logs whatever they say. They land in `coverage/tinygo-spike/`.

| Stage | Asks |
|-------|------|
| `tinygo-spike-build` | Does TinyGo build the CLI for four unix targets, and how large is the result beside its gc twin? |
| `tinygo-spike-run` | Does the binary run, report its platform, and reach the network? |
| `tinygo-spike-net` | The four network layers one at a time, both toolchains, plus what `-X` does to a version variable and what actually arrives on a TLS socket. |
| `tinygo-spike-fork` | Does the [fork](#the-fork) install and report its version? |
| `tinygo-spike-selfupdate` | Does dispat update itself over real TLS when built by the fork? |
| `tinygo-spike-test` | Does `tinygo test` run each module's unit tests? |

Buildx reaches only linux, so the darwin binaries the spike builds are never executed there. The other half runs
natively on a Mac:

```sh
sh scripts/tinygo-spike-darwin.sh
```

It mirrors the same stages for `darwin/arm64` and, when Rosetta answers, `darwin/amd64`, writing
`coverage/tinygo-spike/darwin-*.log`. It extracts the container's probe programs from the Dockerfile at run time rather
than carrying copies, so the two halves cannot drift apart.

## The verdict, as of TinyGo 0.41.1

**No, and the reason is the network.** TinyGo does not use the host's `net`. It ships a port of the package over
netdev, its network *device driver* interface, which a driver from `tinygo.org/x/drivers` installs at startup. On a
host operating system there is no such driver, so the default netdev is a stub: DNS resolution and TLS both return
`Netdev not set`, and `net/http` dereferences nil and aborts.

That is every release path dispat has: the GitHub API, webhooks, self-update's download, the update check. So nothing
is built with it. The other answers were good, which is why the file is kept rather than deleted.

The spike also found the linker difference the CLI's own source now records: upstream TinyGo's `-X` applies only to a
string variable declared with no value, and silently ignores the flag for one declared with a value.
`internal/cli.Version` is therefore declared bare. A stamped version matters more than it looks: a binary reporting
`dev` is a local build to [self-update](../reference/self-update.md), which refuses one before it reaches the network,
so nothing below could be asked at all until this was fixed. The fork stamps both declarations, which the `ldflags` row
records; the bare one is what both toolchains agree on.

### Why a "does https work" check is not enough

A stubbed `crypto/tls` returns no errors. It completes a handshake that never happened and writes plaintext to port
443, and a client asking itself whether the request went well is happy throughout. Only the far end of the wire can
tell the two apart, so the spike holds the socket itself. An assertion server records a connection's first bytes (a
ClientHello opens `0x16 0x03`), then runs a real handshake with the Go compiler's own TLS, and prints both facts. Every
instrument in the spike is built with gc for this reason. The thing measuring TLS is never made of the thing under
measurement.

## The fork

[`github.com/yohimik/tinygo`](https://github.com/yohimik/tinygo) closes both gaps the verdict rests on: a real
`crypto/tls` and a netdev that speaks to the host's sockets. The spike fetches it with dispat's own
[install command](../cli/install.md), which is also how you would install it:

```sh
dispat install yohimik/tinygo --prerelease --release 0.42.0-net.2 \
  --asset 'tinygo{version}.{os}-{arch}.tar.gz' --bin-dir ~/.local --pipe 'tar -xz'
```

The base image stays at upstream 0.41.1 and every stage up to `tinygo-spike-net` still measures it, so the verdict
above keeps its evidence. The two fork stages are the re-asking.

The fetch is a build-time network step, and it sits in a stage of its own so that editing a probe below it never
re-downloads a toolchain. That cuts both ways: no target here aborts, so a *failed* fetch exits 0 like every other
step and is cached as a perfectly valid layer. Re-asking after a release lands wants the layer thrown away:

```sh
docker buildx build --file Dockerfile.tinygo --target tinygo-spike-export \
  --no-cache-filter tinygo-spike-fork --output type=local,dest=coverage/tinygo-spike .
```

`fork.log` says which toolchain was installed, and `selfupdate.log` opens by naming the toolchain its rows are about,
so a run served from a cached failure reports upstream rather than silently passing them off as the fork's.

## Sizes

The size question is what made the network worth proving, so it is measured the same way every time: the same source,
the same four unix targets, both toolchains in one environment, and both stamped. The gc column is the release
pipeline's exact line (`-trimpath -s -w`), the TinyGo column the line a release would replace it with (`-opt=z
-no-debug`). There are two datasets, because the two toolchain generations answer differently and only one of them can
reach a network.

Upstream TinyGo 0.41.1, measured by `tinygo-spike-build` inside the spike's container, whose binaries cannot resolve a
name or open a TLS connection:

```
target                tinygo        gc            ratio
linux/amd64           4628472       10666146      0.434
linux/arm64           4707712       9699490       0.485
darwin/amd64          4787896       10863904      0.441
darwin/arm64          4361072       9912290       0.440
```

The fork at 0.42.0-net.2, which carries a real `net`, `crypto/tls`, `crypto/x509` and process spawning, cross-built for
all four targets from one macOS host, its own toolchain over go1.26.7:

```
target                tinygo        gc            ratio
linux/amd64           6462688       10686626      0.605
linux/arm64           6095688       9699490       0.628
darwin/amd64          6687464       10884416      0.614
darwin/arm64          5749616       9928850       0.579
```

The fork's binaries are the larger of the two TinyGo columns by about 1.5 MB, which is what a real TLS stack and a real
certificate verifier weigh. They are still around 60% of their gc twins.

## The self-update matrix

`tinygo-spike-selfupdate` is the acceptance test the fork exists for: dispat updating *itself*, through the one release
path that touches every layer at once. Listing, download, digest check, a smoke execution of the new binary, and the
atomic swap that keeps the old one as `.backup`.

The server it runs against is `sufake`, generated per run: a CA and a leaf valid for `localhost` and `127.0.0.1`, the
same release listing shape the black-box suite's fixture serves, and a log line for every connection and every request
carrying the negotiated TLS version, cipher and SNI. The client is pointed at the root with `SSL_CERT_FILE`, so the
fork's own x509 root loading is under test too.

| Row | Setup | Expected |
|-----|-------|----------|
| zero | The fork's binary stamped `1.0.0` | Reports `dispat 1.0.0`, not `dev` |
| A | gc control, CA trusted, full update | Exit 0, now `1.1.0` |
| B | Fork `--check`, CA trusted | Exit 1, pending, API paths only |
| B2 | The same by IP literal | Exit 1, SNI waived by the client |
| C | Fork full update, CA trusted | Exit 0, binary `1.1.0`, backup `1.0.0` |
| C2 | `--rollback`, nothing listening | Exit 0, binary `1.0.0` again |
| D | C's setup with the CA withheld | Nonzero, a certificate error |
| E | Plain HTTP at the TLS port | Nonzero, refused by the listener |
| F | The net stage's layers, re-asked with the fork | Recorded |

Rows D and E are the ones a stub cannot survive. D fails only if the client really verified a chain against real roots,
which a no-op handshake never does; E is refused by the listener itself, and the connection log shows the request
method's bytes where a ClientHello belongs.

On macOS the two toolchains need not agree about where roots come from: a darwin build from the Go compiler hands
certificate verification to the platform verifier rather than reading `SSL_CERT_FILE`, so a generated CA can be
invisible to a trusted row even though the file is perfectly good. The darwin script probes that with both a Go client
and curl, records what each said, and never modifies the keychain: trusting a generated root to make a row pass would
measure the modification rather than the toolchain. Read the trusted rows against that answer, and compare the gc
control with the fork row to tell a verifier's refusal from a toolchain's failure. D and E hold either way.

## Reading the logs

The logs are the artefact. Each is a sequence of `=== what ===` headers followed by the step's output and its
`exit=` status, so a step that failed says so where it happened rather than stopping the run. `fork.log` carries the
toolchain version that was installed; `selfupdate.log` carries the matrix above, with `sufake:` lines interleaved from
the far end of the wire.

Nothing here gates a release, and nothing in CI runs it. Re-asking the question is bumping one version number in
`Dockerfile.tinygo` and running one command.
