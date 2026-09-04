# The TinyGo spike

Read this page to understand why dispat's six release binaries are built with the Go compiler, and why a release also
carries two linux binaries built by a TinyGo fork at roughly 60% of the size. It describes a spike: an experiment kept
in the repository because its answer is a version number away from changing, not a gate any job runs.

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
| `tinygo-spike-test` | Does `tinygo test` run each package's unit tests, one package at a time under a bound? |

Buildx reaches only linux, so the darwin binaries the spike builds are never executed there. The other half runs
natively on a Mac:

```sh
sh scripts/tinygo-spike-darwin.sh
```

It mirrors the same stages for `darwin/arm64` and, when Rosetta answers, `darwin/amd64`, writing
`coverage/tinygo-spike/darwin-*.log`. It extracts the container's probe programs from the Dockerfile at run time rather
than carrying copies, so the two halves cannot drift apart.

## The verdict, as of TinyGo 0.42.0

**No, and the reason is the TLS.** TinyGo does not use the host's `net`. It ships a port of the package over netdev,
its network *device driver* interface. Up to 0.41.1 no driver existed for a host operating system, so DNS resolution
and TLS both returned `Netdev not set` and `net/http` dereferenced nil and aborted. 0.42.0 carries a host netdev on
linux, and the `tcp` row of `net.log`, which resolves `api.github.com` first, and the `http` row pass where they
crashed before. What it does not carry is a
`crypto/tls`: the package is still the offload stub that hands the handshake to a device that is not there, and the
stub does not fail. `tls.Dial` returns a nil error. An `https` request to `api.github.com` ends in `unexpected EOF`,
because the server closed the connection on the plaintext it received. The `tls-reality` row, read from the far end
of the wire, records `first-bytes=504c41 clienthello=false`: the probe's own payload, `PLAINTEXT-LEAK`, byte for byte.
`run.log` says the same of the CLI: `self-update --check` fails with `listing releases: unexpected EOF` where the gc
twin lists the releases.

That is every release path dispat has: the GitHub API, webhooks, self-update's download, the update check, all of
them https. So nothing is built with it. A silent stub is a worse failure than 0.41.1's crash, since a program asking
itself whether the request went well is satisfied all the way to a plaintext socket, which is why the spike holds the
socket itself (below). The other answers were good, which is why the file is kept rather than deleted.

The unit-test stage runs with the upstream toolchain, one package at a time, under three bounds that each answer a way
the row used to report one fact instead of a matrix. `-p 2`, because `tinygo test ./...` compiles one test binary per
core and 0.42.0 exhausts 16 GB compiling the CLI's twenty that way where 0.41.1 fitted, so the stage was killed rather
than answered. `-tags safe`, because testify's go-spew reaches into `reflect.Value`'s unexported flag field through
`unsafe` and panics at init when TinyGo's reflect has none, which took every package importing testify down before its
first test; the tag selects go-spew's reflect-only path, so the answer is the tests' own. And an external `timeout` per
package, because TinyGo's `httptest.Server.Close` waits forever for connections that are never counted and
`-timeout` is not enforced on a hung binary; a hang records exit 124 and the next package still runs.

What the matrix says at 0.42.0: `pkg/manifest`, `pkg/models`, `pkg/scanner` and `pkg/writer` pass; `pkg/ccme` fails
its allocation budget (1632 bytes per parse where gc stays under 1500; a different allocator, not a wrong answer). Of
the CLI's nineteen packages with tests, nine pass (`changelog`, `cond`, `filter`, `fsx`, `globx`, `graph`, `ignore`,
`model`, `plan`); five hang in `httptest` (`app`, `cli`, `github`, `selfupdate`, `webhook`); `config` panics on a
nil pointer; and `gitx`, `install`, `release` and `script` fail every test that spawns a process, with
`files setting not implemented` or `directory setting not implemented`, since upstream's `os.StartProcess` honours
neither `ProcAttr.Files` nor `ProcAttr.Dir`. The stage measures upstream only; the fork's unit-test answer is the
black-box suite's, [below](#what-0420-net4-answered).

The spike also found the linker difference the CLI's own source now records: TinyGo 0.41.1's `-X` applied only to a
string variable declared with no value, and silently ignored the flag for one declared with a value.
`internal/cli.Version` is therefore declared bare. A stamped version matters more than it looks: a binary reporting
`dev` is a local build to [self-update](../reference/self-update.md), which refuses one before it reaches the network,
so nothing below could be asked at all until this was fixed. 0.42.0 stamps both declarations, as the fork always did,
which the `ldflags` row records; the bare one is what every toolchain agrees on.

### Why a "does https work" check is not enough

A stubbed `crypto/tls` returns no errors. It completes a handshake that never happened and writes plaintext to port
443, and a client asking itself whether the request went well is happy throughout. Only the far end of the wire can
tell the two apart, so the spike holds the socket itself. An assertion server records a connection's first bytes (a
ClientHello opens `0x16 0x03`), then runs a real handshake with the Go compiler's own TLS, and prints both facts. Every
instrument in the spike is built with gc for this reason. The thing measuring TLS is never made of the thing under
measurement.

## The fork

[`github.com/yohimik/tinygo`](https://github.com/yohimik/tinygo) closes the gap the verdict rests on, a real
`crypto/tls` with a real certificate verifier, over a netdev that speaks to the host's sockets on linux and darwin
both. The spike fetches it with dispat's own [install command](../cli/install.md), which is also how you would install
it:

```sh
dispat install yohimik/tinygo --prerelease \
  --asset 'tinygo{version}.{os}-{arch}.tar.gz' --bin-dir ~/.local --pipe 'tar -xz'
```

The line names no version on purpose: it installs the fork's newest release, which is what the release's
`tiny-toolchain` stage in `services/dispat/Dockerfile`, the spike's `tinygo-spike-fork` stage and the darwin half of
the spike all run, each with its own `--bin-dir`, so a new fork release is what every one of them asks about next. The
same line without a folder is one of the two in
[`scripts/install-tools.sh`](https://github.com/yohimik/dispat/blob/main/scripts/install-tools.sh), the repository's
[install manifest](../cli/install.md#install-manifests-as-shell-scripts) for a runner; a build that must hold one
particular fork adds `--release`.

A `--pipe` install unpacks a tree rather than writing one file, so there is no destination for dispat to compare a
checksum against: every run fetches the tarball again, and the darwin spike keeps the tree it unpacked in its cache
until `TINYGO_REFRESH=1` asks for the newest one.

The base image is upstream 0.42.0 and every stage up to `tinygo-spike-net` measures it, so the verdict above keeps
its evidence. The two fork stages are the re-asking.

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
-no-debug`). There are two datasets, because the two toolchains answer differently and only one of them speaks TLS.

Upstream TinyGo 0.42.0, measured by `tinygo-spike-build` inside the spike's container (its image carries go1.27.0,
which is the gc column), whose binaries open a TCP connection but send plaintext where TLS belongs:

```
target                tinygo        gc            ratio
linux/amd64           5162816       11165856      0.462
linux/arm64           4972888       10092704      0.493
darwin/amd64          5303376       11385936      0.466
darwin/arm64          4650992       10368210      0.449
```

The fork at 0.43.0-net.1, which carries a real `net`, `crypto/tls`, `crypto/x509`, process spawning and signal
delivery, measured by the darwin script on a Mac, its own toolchain over go1.26.7:

```
target                tinygo        gc            ratio
darwin/amd64          6872520       11027408      0.623
darwin/arm64          5912768       10031666      0.589
```

The fork's binaries are the larger of the two TinyGo columns by 1.3 to 1.6 MB, which is what a real TLS stack and a
real certificate verifier weigh. They are still around 60% of their gc twins. The linux pair the container builds with
the fork is measured for the self-update matrix rather than for size, so the fork's table is the darwin one.

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

## What 0.42.0-net.4 answered

The fork's releases are numbered after the upstream they sit on: `X.Y.Z-net.N` is the fork's Nth release rebased on
upstream `X.Y.Z`, and while one is published upstream has not released `X.Y.Z`. The pinned 0.43.0-net.1 is net.4's
content rebased on upstream 0.42.0, with no change of its own, and the matrix below is what it reports at that pin:
`fork.log` names it, `darwin-net.log` is byte for byte what net.4 wrote, and every self-update row on linux and darwin
lands where it landed. The narrative keeps net.4's name because that is where the answers were found.

The matrix passes at 0.42.0-net.4, as it first did at 0.42.0-net.2: the fork implements `os.StartProcess` over
`posix_spawn`, so C downloads the release, executes the new binary as its own smoke check, swaps it in and keeps the
old one, and C2 puts the old one back with nothing listening. It passes on `linux/arm64` in the container and on both
`darwin/arm64` and `darwin/amd64` on a Mac. Rows D and E hold, and F walks the network layers again with a real
handshake read from the far end of the wire.

The matrix is not the whole product, and the question a release turns on is the wider one: the black-box integration
suite run against a fork-built binary rather than a gc one. That suite found four faults at 0.42.0-net.2, and all four
are closed by net.4.

**Process groups are honoured.** `os.StartProcess` accepts `SysProcAttr.Setpgid` through `POSIX_SPAWN_SETPGROUP`, so
dispat puts every script it runs into its own process group as it always has. Any other `Sys` field still fails, but it
fails closed and names the field it could not honour. The 275 "sys setting not implemented" failures the suite reported
at net.2 are gone.

**Redirects are followed.** `net/http` carries the redirect loop again: ten hops, 301, 302 and 303 rewritten to GET,
307 and 308 replayed through `GetBody`, and credentials stripped when a hop crosses hosts. Every GitHub release asset
redirects to a content host, so this is what `dispat install` and self-update's download step need against real GitHub.

**The dispatch deadlock is fixed.** The intermittent stop in the concurrent dispatch was two faults: an upstream
`RWMutex` that conflated readers holding the lock with readers queued for it, and a darwin `fcntl` variadic call that
passed its argument wrongly and so broke `CloseOnExec` and descriptor inheritance. The suite runs to completion.

**Signals are delivered.** At net.3 a fork-built binary installed the operating system handler `signal.Notify` asked
for, which was enough to suppress the default disposition, and then delivered nothing to the channel: a fork-built
dispat neither shut down gracefully nor died, and only `SIGKILL` ended it. The receiving goroutine parked itself and
the only thing that resumed it was the cooperative scheduler's idle hook, which a hosted target running under
`-scheduler=threads` never calls. net.4 gives the receiver a futex of its own, woken by the handler on the protocol the
handler already ran, and gives `signal.Stop` a futex rather than a spin. `SIGINT` and `SIGTERM` now reach
`signal.NotifyContext`, which is the one signal call dispat makes.

Against a fork-built `darwin/arm64` binary the black-box suite reports 543 tests, 694 counting subtests, all passing,
none failing, in 173 seconds, with one skip: the darwin trust row described above, which the platform verifier decides
rather than the toolchain. That includes the four tests an interrupt has to satisfy, since it must reach the in-flight
script, cancel the packages behind it, report the outcome to a webhook listener and give the release lock back. A
graceful shutdown under the fork binary completes in 3.2 seconds.

So the fork gives all of it: real sockets, real TLS, real certificate verification, real process spawning, real signal
delivery, and a suite of 694 black-box assertions that pass against it, in a binary around 60% of gc's size. That is
what puts `dispat-tiny-linux-amd64` and `dispat-tiny-linux-arm64` on a release beside the six the self-update contract
names.

### Caveats a fork-built release carries

Carried rather than fixed, because none of them blocks the matrix or the suite, and all of them change what a reader
should expect:

- **`http.Client.Timeout` arms nothing.** The fork's `net/http` keeps the field and decorates an error with it, but no
  clock is started from it, so a server that accepts a connection and then says nothing can block a download for as
  long as it likes. dispat sets that timeout on its self-update and install clients, and under a fork build it is
  inert.
- **Socket deadlines stretch.** The netdev `Recv` path restarts `SO_RCVTIMEO` after `EINTR`, so a read deadline is
  measured per uninterrupted attempt rather than once. Under garbage collection pressure a deadline can outlast the
  duration it was set to.
- **A rare hang under emulation.** Roughly one run in three hundred hangs on `amd64` under QEMU, in the emulator's
  futex rather than in the program. It is not reproducible on native hardware, and the fork's own release notes carry
  it.
- **darwin roots reach `net/http` only.** On macOS the fork loads the platform's certificate roots for `net/http` but
  not for a bare `crypto/tls` dial, which is why `darwin-net.log` shows an `https` request returning 200 beside a raw
  TLS row refusing the same certificate. dispat only ever reaches TLS through `net/http`, so nothing in dispat sees
  this.
- **`signal.Ignored` does not link.** The runtime has never implemented `os/signal.signal_ignored`, so a program
  calling it fails at link time. dispat's only signal call is `signal.NotifyContext`, so nothing in dispat reaches it.

## Reading the logs

The logs are the artefact. Each is a sequence of `=== what ===` headers followed by the step's output and its
`exit=` status, so a step that failed says so where it happened rather than stopping the run. `fork.log` carries the
toolchain version that was installed; `selfupdate.log` carries the matrix above, with `sufake:` lines interleaved from
the far end of the wire.

Nothing here gates a release, and nothing in CI runs it. Re-asking the question is running one command: the spike
installs the fork's newest release, as the release's own toolchain stage does, so a new fork release is asked about the
next time either of them runs.
