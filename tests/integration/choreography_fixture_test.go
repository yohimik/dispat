// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// The fixture every choreographed scenario is built from: a fleet of peer
// repositories, each with its own remote, its own configuration and its own
// packages, joined by two-sided submodule links.
//
// It is assembled the way an operator assembles one — a repository at a time,
// pushed to its remote, linked to its neighbours — because the thing under
// test is what a release reads out of those checkouts. Link creation goes
// through plain Git here and through `dispat compute` in the compute
// scenarios alone, so a defect in compute cannot make every other test pass.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// choreographyPeer is one repository of a fleet: its checkout, the remote it
// publishes to, and what it calls itself.
type choreographyPeer struct {
	*harness.Repo
	name   string
	remote string
	pkg    string
}

// choreographyFleet is a whole fleet, in the order its members were declared.
type choreographyFleet struct {
	t     *testing.T
	peers map[string]*choreographyPeer
	names []string
}

// fileProtocolEnv is what lets a test's local paths be used as submodule
// URLs. Git refuses the file transport for submodules unless it is asked, and
// dispat never asks: the product must work against real remotes, so the
// permission belongs in the invocation's environment and nowhere else.
func fileProtocolEnv() []string {
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=protocol.file.allow",
		"GIT_CONFIG_VALUE_0=always",
	}
}

// newChoreographyFleet creates one repository per name, each holding a
// package of the same name, each pushed to its own bare remote, and each
// declaring the whole fleet in its roster.
func newChoreographyFleet(t *testing.T, names ...string) *choreographyFleet {
	t.Helper()
	fleet := &choreographyFleet{t: t, peers: map[string]*choreographyPeer{}, names: names}
	for _, name := range names {
		repo := harness.New(t)
		peer := &choreographyPeer{Repo: repo, name: name, pkg: name + "-pkg"}
		peer.SeedPackage("packages", peer.pkg)
		peer.remote = peer.AddBareRemote()
		fleet.peers[name] = peer
	}
	for _, name := range names {
		fleet.writeConfig(name, func(*models.File) {})
		fleet.peers[name].Commit("feat(" + fleet.peers[name].pkg + "): bootstrap " + name)
		fleet.push(name)
	}
	return fleet
}

// peer answers one member of the fleet.
func (f *choreographyFleet) peer(name string) *choreographyPeer {
	f.t.Helper()
	peer, ok := f.peers[name]
	require.True(f.t, ok, "fleet has no repository %q", name)
	return peer
}

// config is the configuration one peer starts with: the saga, its identity,
// the roster of everyone else, its own space and the release flow every
// scenario shares.
func (f *choreographyFleet) config(name string) models.File {
	cfg := harness.BaseFile(2)
	cfg.Repository = name
	for _, other := range f.names {
		if other == name {
			continue
		}
		cfg.Repositories = append(cfg.Repositories, models.RepositoryLinkConfig{
			Name: other, URL: f.peer(other).remote, Branch: harness.DefaultBranch,
		})
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		name: {Path: models.PathList{"packages"}},
	}
	cfg.Scripts = map[string]models.Script{
		"build":   {"echo building $DISPAT_PACKAGE"},
		"publish": {"echo publishing $DISPAT_PACKAGE"},
	}
	cfg.Flow = &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}}
	// A release writes a changelog entry, so every published package leaves a
	// release commit behind. That commit is what carries a fleet link's
	// evidence, and a fleet whose releases wrote nothing could not record any.
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Branch: harness.DefaultBranch}
	return cfg
}

// writeConfig writes one peer's configuration, after the scenario has adjusted
// it. Nothing is committed: the caller decides when that happens.
func (f *choreographyFleet) writeConfig(name string, adjust func(*models.File)) {
	f.t.Helper()
	cfg := f.config(name)
	adjust(&cfg)
	f.peer(name).WriteConfigModel(cfg)
}

// push publishes a peer's branch to its own remote, which is what makes its
// revisions fetchable by the repositories that link it.
func (f *choreographyFleet) push(name string) {
	f.t.Helper()
	f.peer(name).Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
}

// link joins two peers, both ways: each one holds a checkout of the other at
// the default fleet path. This is the state `dispat compute` produces, made
// here with plain Git so the scenarios that are not about compute do not
// depend on it.
func (f *choreographyFleet) link(a, b string) {
	f.t.Helper()
	f.linkOneWay(a, b)
	f.linkOneWay(b, a)
	// The first half was created before the second existed, so the checkout
	// it made predates the link back. Following the peer's branch and
	// recording where it landed is what an operator does next, and it is what
	// leaves both ends holding a fleet that knows about both of them.
	f.follow(a, b)
}

// follow brings one peer's checkout of another up to its remote branch tip
// and records the new pin, which is the ordinary way a fleet member catches
// up with a neighbour.
func (f *choreographyFleet) follow(from, to string) {
	f.t.Helper()
	f.refresh(from, to)
	if f.peer(from).Git("status", "--porcelain=v1") == "" {
		return
	}
	f.peer(from).Commit("chore: follow " + to)
	f.push(from)
}

// linkOneWay creates the half of a link one repository holds.
func (f *choreographyFleet) linkOneWay(from, to string) {
	f.t.Helper()
	peer, target := f.peer(from), f.peer(to)
	path := ".links/" + to
	peer.Git("-c", "protocol.file.allow=always", "submodule", "add", "-q",
		"--name", to, "-b", harness.DefaultBranch, "--", target.remote, path)
	peer.Git("-C", path, "config", "user.email", "integration@dispat.test")
	peer.Git("-C", path, "config", "user.name", "dispat integration")
	peer.Commit("chore: link " + to)
	f.push(from)
}

// materialize initializes a link inside another link's checkout, which is how
// a fleet of more than two repositories is made ready to release: every hop
// of the route has to be a real checkout, and nothing does that recursively
// on its own.
func (f *choreographyFleet) materialize(entry *harness.Repo, path, link string) {
	f.t.Helper()
	entry.Git("-C", path, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--", ".links/"+link)
	entry.Git("-C", filepath.Join(path, ".links", link), "config", "user.email", "integration@dispat.test")
	entry.Git("-C", filepath.Join(path, ".links", link), "config", "user.name", "dispat integration")
}

// refresh brings one peer's checkout of another up to that repository's
// remote branch tip, which is what a fleet member does before releasing.
func (f *choreographyFleet) refresh(from, to string) {
	f.t.Helper()
	f.peer(from).Git("-c", "protocol.file.allow=always", "submodule", "update", "--remote", "--", ".links/"+to)
}

// enter is a fresh clone of one peer with its links materialized, and nothing
// else: the checkout a CI runner releases from. The update is deliberately
// not recursive, so the copy of this repository inside each link stays the
// empty folder a fleet leaves it as.
func (f *choreographyFleet) enter(name string) *harness.Repo {
	f.t.Helper()
	peer := f.peer(name)
	clone := harness.Clone(f.t, peer.remote)
	for _, other := range f.names {
		if other == name {
			continue
		}
		if !f.isLinked(name, other) {
			continue
		}
		clone.Git("-c", "protocol.file.allow=always", "submodule", "update", "--init", "--", ".links/"+other)
		clone.Git("-C", ".links/"+other, "config", "user.email", "integration@dispat.test")
		clone.Git("-C", ".links/"+other, "config", "user.name", "dispat integration")
	}
	return clone
}

// isLinked reports whether one peer declares a link to another.
func (f *choreographyFleet) isLinked(from, to string) bool {
	f.t.Helper()
	data, err := os.ReadFile(f.peer(from).Path(".gitmodules"))
	if os.IsNotExist(err) {
		return false
	}
	require.NoError(f.t, err)
	return strings.Contains(string(data), "submodule \""+to+"\"")
}

// work adds a commit to one peer's own package and pushes it, which is the
// change a release is computed from.
func (f *choreographyFleet) work(name, message string) string {
	f.t.Helper()
	peer := f.peer(name)
	peer.WriteFile(filepath.Join("packages", peer.pkg, "work.txt"), message+"\n")
	peer.Commit(message)
	f.push(name)
	return peer.Git("rev-parse", "HEAD")
}

// workOnly commits a change to one peer's own package without staging
// anything else, which is how a scenario keeps an advisory link drift out of
// the commit it is about to release.
func (f *choreographyFleet) workOnly(name, message string) {
	f.t.Helper()
	peer := f.peer(name)
	peer.WriteFile(filepath.Join("packages", peer.pkg, "work.txt"), message+"\n")
	peer.Git("add", filepath.Join("packages", peer.pkg))
	peer.Git("commit", "-q", "-m", message)
	peer.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
}

// workIn adds a commit to a peer's checkout that lives inside another peer,
// and pushes it from there: the linked checkout is the working tree a run
// started in that repository actually reads.
func (f *choreographyFleet) workIn(entry *harness.Repo, link, pkg, message string) string {
	f.t.Helper()
	dir := ".links/" + link
	entry.WriteFile(filepath.Join(dir, "packages", pkg, "work.txt"), message+"\n")
	entry.Git("-C", dir, "add", "-A")
	entry.Git("-C", dir, "commit", "-q", "-m", message)
	entry.Git("-C", dir, "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	return entry.Git("-C", dir, "rev-parse", "HEAD")
}

// configureIn writes one peer's configuration inside another peer's checkout
// of it, commits it there and pushes it: the linked checkout is the working
// tree a run started in that repository reads, so that is where a scenario
// changes what the peer says about itself.
func (f *choreographyFleet) configureIn(entry *harness.Repo, link, message string, adjust func(*models.File)) {
	f.t.Helper()
	cfg := f.config(link)
	adjust(&cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(f.t, err)
	entry.WriteFile(filepath.Join(".links", link, "dispat.json"), string(data))
	entry.Git("-C", ".links/"+link, "add", "-A")
	commitLinked(entry, ".links/"+link, message)
	entry.Git("-C", ".links/"+link, "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
}

// tagsIn lists the release tags of a repository inside another checkout.
func tagsIn(repo *harness.Repo, relPath string) []string {
	out := repo.Git("-C", relPath, "tag", "--list")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// gitlinkAt is the revision one tree records for a fleet link.
func gitlinkAt(repo *harness.Repo, revision, path string) string {
	out := repo.Git("ls-tree", revision, "--", path)
	fields := strings.Fields(out)
	if len(fields) < 3 {
		return ""
	}
	return fields[2]
}

// subjects is one repository's commit subjects, newest first.
func subjects(repo *harness.Repo, args ...string) []string {
	out := repo.Git(append([]string{"log", "--format=%s"}, args...)...)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// releaseFlow is the script pair a scenario overrides when it needs a release
// stage to do something of its own.
func releaseFlow(build, publish string) map[string]models.Script {
	return map[string]models.Script{"build": {build}, "publish": {publish}}
}

// shellQuote renders a path as one shell word, for the scripts a scenario
// writes into a configuration.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// readAbs is a scenario reading what a script or a release wrote, at an
// absolute path rather than inside one repository.
func readAbs(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// requireDiagnostic asserts that a run reported one diagnostic code.
//
// A plan's diagnostics are JSON events on stdout, which is what most
// assertions read. A configuration that never loaded is reported by the boot
// logger, which writes to stderr and, without an explicit --log-format, in
// the console format: there is no configuration yet to say otherwise. The
// code is asserted either way, never the prose around it.
func requireDiagnostic(t *testing.T, res harness.RunResult, code string) {
	t.Helper()
	if harness.IsCodePresent(res.Events, code) {
		return
	}
	require.Contains(t, res.Stdout+res.Stderr, code,
		"no %s diagnostic\nstdout:\n%s\nstderr:\n%s", code, res.Stdout, res.Stderr)
}

// requireNoDiagnostic is its opposite, for the states a fleet must not report.
func requireNoDiagnostic(t *testing.T, res harness.RunResult, code string) {
	t.Helper()
	require.False(t, harness.IsCodePresent(res.Events, code),
		"unexpected %s\nstdout:\n%s", code, res.Stdout)
}

// handshakeScript is the bounded file handshake two concurrent publishes use
// to prove they overlap: each writes its own marker and waits for the other's,
// giving up after a bounded number of attempts rather than hanging.
func handshakeScript(marker, peer string) string {
	return fmt.Sprintf(`: > %q; attempts=0; `+
		`while [ ! -f %q ] && [ "$attempts" -lt 300 ]; do attempts=$((attempts + 1)); sleep 0.01; done; `+
		`test -f %q`, marker, peer, peer)
}

// canonicalRoot is the path dispat spells a repository's root as: absolute
// with symlinks resolved, because a temporary directory on macOS is reached
// through one. A fault glob naming a repository has to use this spelling, and
// naming the root exactly is what separates a repository from the checkouts
// of its peers inside it.
func canonicalRoot(t *testing.T, repo *harness.Repo) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(repo.Root)
	require.NoError(t, err)
	return resolved
}

// commitLinked commits what is staged inside a linked checkout, carrying the
// identity on the command. A checkout `dispat compute` cloned has no identity
// of its own, and a CI container has no global one to fall back on, so a bare
// commit there passes on a developer's machine and fails under the release
// image.
func commitLinked(repo interface{ Git(args ...string) string }, relPath, message string) {
	repo.Git("-C", relPath, "-c", "user.email=integration@dispat.test", "-c", "user.name=dispat integration",
		"commit", "-q", "-m", message)
}
