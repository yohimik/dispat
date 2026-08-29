#!/bin/sh
# The darwin half of the TinyGo spike. Buildx only reaches linux, so the
# darwin binaries Dockerfile.tinygo builds have never been *run*; this script
# mirrors its build, run and net stages natively on a Mac, for darwin/arm64
# and — when Rosetta answers — darwin/amd64. Same philosophy as the
# Dockerfile: no step aborts the spike, every step records its own exit
# status, and the logs are the artefact, written beside the container's under
# coverage/tinygo-spike/ as darwin-{build,sizes,run,net}.log.
#
# The probe and assertion programs are the Dockerfile's own heredocs,
# extracted from it at run time rather than copied here, so the two halves of
# the spike can never drift apart and the instruments stay what they are
# there: the spike's, not the repository's code.
#
# Toolchains are fetched on first use into a cache folder and reused after:
# Go from go.dev, pinned below with its published checksums, and TinyGo from
# the fork's GitHub release through `dispat install yohimik/tinygo` — the
# same fetch the container spike makes, exercised here as a user would type
# it. Re-asking the question is bumping the one TINYGO_VERSION number.
#
# Requirements: macOS, dispat on PATH, git checkout, network.
set -u

# The pins.
TINYGO_VERSION=0.42.0-net.1
GO_VERSION=1.26.7
GO_SHA256_ARM64=020a1e8224811be75163e920bc77e0926a1390a6aeea19bdcf23f74b9d749f6d
GO_SHA256_AMD64=92e8b34bff3c89ab16404c595669ac8cb004cc2f676dcbd1f5b87a6b8def3b47

die() {
	echo "tinygo-spike-darwin: $*" >&2
	exit 1
}

[ "$(uname -s)" = Darwin ] || die "this half runs on macOS; the linux half is Dockerfile.tinygo"
command -v dispat >/dev/null 2>&1 || die "dispat is not on PATH; https://dispat.dev/reference/ci/#the-install-script"

root=$(cd "$(dirname "$0")/.." && pwd)
cache="${XDG_CACHE_HOME:-$HOME/.cache}/tinygo-spike-darwin"
spike="$root/coverage/tinygo-spike"
out="$cache/out"
mkdir -p "$cache" "$spike" "$out"

case "$(uname -m)" in
arm64) host_arch=arm64 go_sha="$GO_SHA256_ARM64" ;;
x86_64) host_arch=amd64 go_sha="$GO_SHA256_AMD64" ;;
*) die "unexpected host arch $(uname -m)" ;;
esac

# --- the toolchains, fetched once ---------------------------------------------

# Go first: TinyGo refuses to run without a host Go, and the gc twins are the
# control every probe is read against.
if [ ! -x "$cache/go/bin/go" ]; then
	echo "fetching go$GO_VERSION darwin-$host_arch" >&2
	curl -fsSL -o "$cache/go.tar.gz" "https://go.dev/dl/go$GO_VERSION.darwin-$host_arch.tar.gz" \
		|| die "go download failed"
	echo "$go_sha  $cache/go.tar.gz" | shasum -a 256 -c - >/dev/null || die "go tarball checksum mismatch"
	tar -xzf "$cache/go.tar.gz" -C "$cache" && rm "$cache/go.tar.gz"
fi
export PATH="$cache/go/bin:$cache/tinygo/bin:$PATH"
export GOPATH="$cache/gopath"

# TinyGo from the fork's release, through the command under test. --pipe
# extracts the whole toolchain tree rather than installing one binary, because
# tinygo is its bin/ plus the lib/ and src/ beside it.
if ! "$cache/tinygo/bin/tinygo" version 2>/dev/null | grep -qF "$TINYGO_VERSION"; then
	rm -rf "$cache/tinygo"
	echo "fetching tinygo $TINYGO_VERSION via dispat install" >&2
	dispat install yohimik/tinygo --prerelease --release "$TINYGO_VERSION" \
		--asset 'tinygo{version}.{os}-{arch}.tar.gz' \
		--bin-dir "$cache" --pipe 'tar -xz' \
		|| die "dispat install yohimik/tinygo failed (is v$TINYGO_VERSION published?)"
	"$cache/tinygo/bin/tinygo" version | grep -qF "$TINYGO_VERSION" || die "fetched tinygo reports the wrong version"
fi

# The same bypass and kill switch the container sets.
export GOPRIVATE=github.com/yohimik/dispat
export DISPAT_UPDATE_CHECK=false

# --- the build probe and the size record (mirrors tinygo-spike-build) ---------

log="$spike/darwin-build.log"
: >"$log"
cd "$root/services/dispat"
V=github.com/yohimik/dispat/services/dispat/internal/cli.Version
for arch in amd64 arm64; do
	echo "=== tinygo build darwin/$arch ===" >>"$log"
	GOOS=darwin GOARCH="$arch" tinygo build -opt=z -no-debug \
		-ldflags="-X $V=0.0.0-spike" \
		-o "$out/tinygo-dispat-darwin-$arch" . >>"$log" 2>&1
	echo "exit=$?" >>"$log"
	echo "=== gc build darwin/$arch ===" >>"$log"
	GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
		-ldflags "-s -w -X $V=0.0.0-spike" \
		-o "$out/gc-dispat-darwin-$arch" . >>"$log" 2>&1
	echo "exit=$?" >>"$log"
done
{
	echo "target                tinygo        gc            ratio"
	for arch in amd64 arm64; do
		t=$(wc -c <"$out/tinygo-dispat-darwin-$arch" 2>/dev/null || echo 0)
		g=$(wc -c <"$out/gc-dispat-darwin-$arch" 2>/dev/null || echo 0)
		if [ "$g" -gt 0 ] && [ "$t" -gt 0 ]; then
			r=$(awk -v t="$t" -v g="$g" 'BEGIN{printf "%.3f", t/g}')
		else
			r="n/a"
		fi
		printf '%-20s  %-12s  %-12s  %s\n' "darwin/$arch" "$t" "$g" "$r"
	done
} >"$spike/darwin-sizes.log"
cat "$log" "$spike/darwin-sizes.log"

# The arches this host can execute: its own always, the other through
# Rosetta when installed. A skipped arch is recorded, not silent.
runnable=$host_arch
if [ "$host_arch" = arm64 ] && arch -x86_64 /usr/bin/true 2>/dev/null; then
	runnable="arm64 amd64"
fi

# --- the execution probe (mirrors tinygo-spike-run) ---------------------------

log="$spike/darwin-run.log"
: >"$log"
echo "=== host arch: $host_arch, runnable: $runnable ===" >>"$log"
for arch in $runnable; do
	bin="$out/tinygo-dispat-darwin-$arch"
	echo "=== $bin --version ===" >>"$log"
	"$bin" --version >>"$log" 2>&1
	echo "exit=$?" >>"$log"
	echo "=== darwin/$arch https probe: self-update --check ===" >>"$log"
	DISPAT_UPDATE_CHECK=true "$bin" self-update --check --log-format json >>"$log" 2>&1
	echo "exit=$?" >>"$log"
	echo "=== darwin/$arch https probe: gc twin, for comparison ===" >>"$log"
	DISPAT_UPDATE_CHECK=true "$out/gc-dispat-darwin-$arch" self-update --check --log-format json >>"$log" 2>&1
	echo "exit=$?" >>"$log"
done
cat "$log"

# --- the bare net stack and the tls-reality rounds (mirrors tinygo-spike-net) -

work=$(mktemp -d) || die "mktemp failed"
trap 'rm -rf "$work"' EXIT

# The instruments, verbatim from Dockerfile.tinygo.
mkdir "$work/netprobe" "$work/tlsreality"
sed -n '/^COPY --chown=gopher:gopher <<.PROBE. /,/^PROBE$/p' "$root/Dockerfile.tinygo" \
	| sed '1d;$d' >"$work/netprobe/main.go"
sed -n '/^COPY --chown=gopher:gopher <<.TLSREALITY. /,/^TLSREALITY$/p' "$root/Dockerfile.tinygo" \
	| sed '1d;$d' >"$work/tlsreality/main.go"
[ -s "$work/netprobe/main.go" ] || die "failed to extract the netprobe heredoc from Dockerfile.tinygo"
[ -s "$work/tlsreality/main.go" ] || die "failed to extract the tlsreality heredoc from Dockerfile.tinygo"

log="$spike/darwin-net.log"
: >"$log"
export GOWORK=off
cd "$work/netprobe"
printf 'module netprobe\n\ngo 1.24\n' >go.mod
X="-X main.Bare=1.2.3 -X main.Initialed=1.2.3"
for arch in $runnable; do
	echo "=== tinygo build darwin/$arch ===" >>"$log"
	GOOS=darwin GOARCH="$arch" tinygo build -ldflags "$X" -o "$out/netprobe-tinygo-$arch" . >>"$log" 2>&1
	echo "exit=$?" >>"$log"
	echo "=== gc build darwin/$arch ===" >>"$log"
	GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 go build -ldflags "$X" -o "$out/netprobe-gc-$arch" . >>"$log" 2>&1
	echo "exit=$?" >>"$log"
done
echo "=== tls-reality build (gc) ===" >>"$log"
cd "$work/tlsreality"
printf 'module tlsreality\n\ngo 1.24\n' >go.mod
CGO_ENABLED=0 go build -o "$out/tlsreality" . >>"$log" 2>&1
echo "exit=$?" >>"$log"
for arch in $runnable; do
	for toolchain in tinygo gc; do
		for layer in ldflags tcp tls http https; do
			echo "=== $toolchain darwin/$arch $layer ===" >>"$log"
			"$out/netprobe-$toolchain-$arch" "$layer" >>"$log" 2>&1
			echo "exit=$?" >>"$log"
		done
		echo "=== $toolchain darwin/$arch tls-reality ===" >>"$log"
		: >"$work/reality.out"
		"$out/tlsreality" 127.0.0.1:14443 >"$work/reality.out" 2>&1 &
		server=$!
		i=0
		until grep -q listening "$work/reality.out" || [ "$i" -ge 50 ]; do
			i=$((i + 1))
			sleep 0.1
		done
		"$out/netprobe-$toolchain-$arch" tlslocal 127.0.0.1:14443 >>"$log" 2>&1
		wait "$server"
		status=$?
		cat "$work/reality.out" >>"$log"
		echo "exit=$status" >>"$log"
	done
done
cat "$log"

# The spike's whole answer is the logs; a probe that failed already said so
# in them, and gating here would report one fact where the matrix needs all.
exit 0
