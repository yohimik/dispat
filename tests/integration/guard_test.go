// Goal 21: the release guards. `run.allowBranch` refuses a release from any
// branch outside its globs while the read-only commands stay available, and a
// push-mode release from a checkout behind the remote is refused before any
// release work starts. Both refusals fire before builds, tags or pushes, so a
// guarded run leaves the repository exactly as it found it.
package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// pushClone clones the bare remote into a scratch folder and pushes one empty
// commit from it: another checkout moving the branch on, which is what leaves
// the repository under test behind.
//
// Both the branch to clone and the ref to push are named explicitly. The bare
// repository's own HEAD would otherwise follow whatever init.defaultBranch the
// host git is configured with, the clone would land on an unborn branch, and
// the push would create a second branch the original never tracks — so the
// checkout under test would never be behind and the test would prove nothing.
func pushClone(t *testing.T, bare, message string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	git("clone", "-q", "--branch", harness.DefaultBranch, bare, ".")
	git("config", "user.email", "other@dispat.test")
	git("config", "user.name", "other clone")
	git("commit", "-q", "--allow-empty", "-m", message)
	git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
}

// TestGuardAllowBranch: a release on an allowlisted branch proceeds, one on a
// foreign branch is refused with nothing released, a glob reaches slashed
// branch names, and `dispat status` is never guarded.
func TestGuardAllowBranch(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Run = &models.RunConfig{AllowBranch: []string{harness.DefaultBranch, "release/*"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")

	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())

	r.Commit("feat(core): pending work")
	r.Git("checkout", "-q", "-b", "feature/tryout")
	res := r.Release()
	require.Equal(t, 1, res.Code, "a foreign branch must refuse\nstdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, `branch \"feature/tryout\" is not allowed`)
	assert.Contains(t, res.Stdout, "release/*", "the refusal names what would be allowed")
	assert.False(t, r.HasTag("core@0.2.0"), "a refused run must not tag")

	// The guard gates releasing, not reading: the dry run works anywhere.
	r.StatusOK()

	// A slashed branch matching a glob may release.
	r.Git("checkout", "-q", "-b", "release/v1")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.2.0"), "tags: %v", r.TagList())
}

// TestGuardAllowBranchRefusesDetachedHead: a detached HEAD has no branch name,
// so it matches nothing — including a glob as broad as "*".
func TestGuardAllowBranchRefusesDetachedHead(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Run = &models.RunConfig{AllowBranch: []string{"*"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	r.Git("checkout", "-q", r.Git("rev-parse", "HEAD"))

	res := r.Release()
	require.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "HEAD is detached")
	assert.False(t, r.HasTag("core@0.1.0"), "a refused run must not tag")
}

// TestGuardBehindRemote: in push mode a checkout whose branch tip is behind
// the remote refuses *before the plan is computed at all* — a plan built on
// stale tags is wrong rather than merely useless, because it recomputes
// versions somebody else has already published — and releases normally once
// the checkout has caught up.
func TestGuardBehindRemote(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	bare := r.AddBareRemote()

	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	require.Equal(t, 1, buildRuns(r), "the first release built once")

	// The remote moves on without this checkout.
	pushClone(t, bare, "chore: pushed elsewhere")

	r.WriteFile("packages/core/more.txt", "stale work\n")
	r.Commit("feat(core): stale work")
	// A unit addressing nothing: planning this window reports W131 against it.
	// The code is therefore a witness that planning happened, and its absence
	// below is what distinguishes "refused before the plan" from "refused
	// after it".
	r.CommitEmpty("feat(ghost): a scope no package answers to")
	res := r.Release()
	require.Equal(t, 1, res.Code, "a stale checkout must refuse\nstdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "behind origin/"+harness.DefaultBranch)
	assert.False(t, r.HasTag("core@0.2.0"), "a refused run must not tag")
	assert.Equal(t, 1, buildRuns(r), "a refused run runs no build script")
	assert.False(t, harness.HasCode(res.Events, "W131"),
		"the refusal precedes planning, so no planning diagnostic is reported: %v", res.Events)

	// Catching up clears the guard and the release goes through, push and all.
	r.Git("pull", "-q", "--rebase", "origin", harness.DefaultBranch)
	caught := r.ReleaseOK()
	require.True(t, r.HasTag("core@0.2.0"), "tags: %v", r.TagList())
	assert.Contains(t, r.Git("ls-remote", "origin"), "refs/tags/core@0.2.0")
	// The witness, proved rather than assumed: the same window does report
	// W131 once a plan is actually computed, so its absence above was the
	// refusal's doing and not a code this repository never raises.
	assert.True(t, harness.HasCode(caught.Events, "W131"),
		"the inert unit is reported once planning happens: %v", caught.Events)
	assert.Equal(t, 2, buildRuns(r), "and the caught-up run builds")
}

// TestGuardBehindRemoteHonoursCommitVerify: the behind check is another
// ls-remote, so it belongs under the same switch as the reachability check.
// commit.verify=false exists for remotes that reject ls-remote but accept
// pushes, and turning it off has to silence both or it silences neither.
func TestGuardBehindRemoteHonoursCommitVerify(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{
		Enabled: models.Bool(true), Push: true, Verify: models.Bool(false)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	bare := r.AddBareRemote()

	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())

	pushClone(t, bare, "chore: pushed elsewhere")

	r.WriteFile("packages/core/more.txt", "stale work\n")
	r.Commit("feat(core): stale work")
	res := r.Release()
	assert.NotContains(t, res.Stdout, "behind origin/",
		"with verification off the checkout is never compared to the remote")

	// And this is what the guard buys when it is on. The run went all the way
	// through, building, publishing and tagging, against a plan computed from
	// tags this checkout had already stopped being able to see: exactly the
	// release the check exists to prevent from being planned at all.
	//
	// It still lands, because the push it is rejected on is recovered from
	// (see TestReleaseMergesWhatLandedDuringTheRun). What the guard was
	// protecting is the plan, not the push, and the W242 warning is the only
	// thing left saying the tree it went out on is not the one it was planned
	// against.
	assert.Contains(t, res.Stdout, "published")
	assert.True(t, r.HasTag("core@0.2.0"), "the tag was created; tags: %v", r.TagList())
	assert.True(t, harness.HasCode(res.Events, "W242"),
		"the release pulled what it could not see when it planned: %v", res.Events)
	assert.Equal(t, 0, res.Code)
}

// TestGuardsAreUnsetByDefault: neither guard applies to a configuration that
// asks for it, so an ordinary repository — including one on a branch named
// anything at all, pushing to a remote — releases exactly as before.
func TestGuardsAreUnsetByDefault(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	r.AddBareRemote()
	r.Git("checkout", "-q", "-b", "some/odd/branch")

	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
}

// landAgainDuringTheRecovery arms the bare remote to move the branch once more
// underneath the recovery's own push.
//
// A pre-receive hook, because that is the only place something can happen
// between the recovery's fetch and its push: the run's scripts are all
// finished by the time the finalize push happens. The hook lands a commit and
// then declines the update, writing the wording git itself uses when two runs
// push at the same instant. A real race cannot be scheduled, and what is under
// test is what dispat does with the answer rather than how the answer came
// about.
//
// It fires on exactly one push, so the round after it succeeds.
func landAgainDuringTheRecovery(t *testing.T, bare, message string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the hook is a POSIX shell script")
	}
	// The hook counts what reaches it and acts on the second. The first is the
	// build script's own foreign push; the release's rejected push never gets
	// here, because git refuses a non-fast-forward on the client side; so the
	// second is the recovery's, which is the one to move the branch under.
	counter := harness.ShQuote(filepath.Join(bare, "received.count"))
	hook := filepath.Join(bare, "hooks", "pre-receive")
	script := "#!/bin/sh\n" +
		"n=$(cat " + counter + " 2>/dev/null || echo 0)\n" +
		"n=$((n + 1))\n" +
		"echo \"$n\" > " + counter + "\n" +
		"[ \"$n\" -eq 2 ] || exit 0\n" +
		// The receiving process exports its own repository into the
		// environment, and every command below is about another one.
		"unset GIT_DIR GIT_QUARANTINE_PATH GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES\n" +
		"d=$(mktemp -d)\n" +
		"git clone -q --branch " + harness.DefaultBranch + " " + harness.ShQuote(bare) + " \"$d\" || exit 1\n" +
		"printf 'again' > \"$d\"/AGAIN.md\n" +
		"git -C \"$d\" add -A\n" +
		"git -C \"$d\" -c user.email=other@dispat.test -c user.name='other clone' " +
		"commit -q -m " + harness.ShQuote(message) + " || exit 1\n" +
		"git -C \"$d\" push -q origin HEAD:refs/heads/" + harness.DefaultBranch + " || exit 1\n" +
		"echo \"cannot lock ref 'refs/heads/" + harness.DefaultBranch + "': is at another commit\" >&2\n" +
		"exit 1\n"
	require.NoError(t, os.WriteFile(hook, []byte(script), 0o755))
}

// foreignChange is one commit a second clone lands while the release runs.
type foreignChange struct{ message, file, contents string }

// midReleasePush is a build script that pushes a commit from a second clone
// while the release is running: exactly the window the behind-remote guard
// cannot cover, because it closes before the plan exists and this happens
// after it. The file it writes decides whether the release commit can be
// merged with what landed, since a conflict is a conflict over content.
func midReleasePush(t *testing.T, bare, message, file, contents string) string {
	t.Helper()
	return midReleasePushes(t, bare, foreignChange{message, file, contents})
}

// midReleasePushes is midReleasePush with more than one commit, all landed by
// the same clone in one go.
//
// The whole thing fires once. A scenario that releases twice is asking what
// the run after the recovery does, and a second round of foreign pushes would
// answer a different question; the marker sits at the repository root, which
// no release commit stages.
func midReleasePushes(t *testing.T, bare string, changes ...foreignChange) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the mid-release push is a POSIX shell script")
	}
	steps := []string{
		"d=$(mktemp -d)",
		"git clone -q --branch " + harness.DefaultBranch + " " + harness.ShQuote(bare) + " \"$d\"",
	}
	for _, c := range changes {
		steps = append(steps,
			"printf '%s' "+harness.ShQuote(c.contents)+" > \"$d\"/"+c.file,
			"git -C \"$d\" add -A",
			"git -C \"$d\" -c user.email=other@dispat.test -c user.name='other clone' commit -q -m "+
				harness.ShQuote(c.message))
	}
	steps = append(steps, "git -C \"$d\" push -q origin HEAD:refs/heads/"+harness.DefaultBranch)
	return "if [ ! -f ../../pushed.marker ]; then touch ../../pushed.marker && " +
		strings.Join(steps, " && ") + "; fi"
}

// releaseCommit resolves the run's release commit by the subject it was made
// under, which is what a scenario knows about it. Resolving it that way rather
// than as a parent of HEAD is what makes the assertions about it survive a
// recovery that merged more than once.
func releaseCommit(t *testing.T, r *harness.Repo, subject string) string {
	t.Helper()
	log := r.Git("log", "--format=%H %s")
	for _, line := range strings.Split(log, "\n") {
		sha, got, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && got == subject {
			return sha
		}
	}
	t.Fatalf("no commit titled %q; log:\n%s", subject, log)
	return ""
}

// firstParents is the tip's first-parent chain, which is the line a release
// commit merged under has to stay on.
func firstParents(t *testing.T, r *harness.Repo) []string {
	t.Helper()
	return strings.Fields(r.Git("rev-list", "--first-parent", "HEAD"))
}

// TestReleaseMergesWhatLandedDuringTheRun: the recovery at the far end of a
// release. The behind-remote guard closes before the plan is computed, so a
// commit pushed while the run is working reaches the finalize push as a
// rejection, and the run has already published: refusing there would leave a
// released package with no commit, no tag and nothing on the remote.
//
// So the release joins the two rather than choosing between them. Nothing it
// made is rewritten: the release commit keeps its identity and its tag, so the
// tagged tree still carries the changelog and the version rewrites the release
// recorded. Only the branch tip changes, into a merge of the release commit
// and what arrived.
//
// The commit that arrived is therefore outside the tag's ancestry, and that is
// the property this whole shape exists for: it was not in this run's plan, it
// is not in this run's record, and the next run releases it on its own terms.
func TestReleaseMergesWhatLandedDuringTheRun(t *testing.T) {
	r := harness.New(t)
	bare := r.AddBareRemote()
	srv, bodies := githubFake(t)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	cfg := libsConfig(midReleasePush(t, bare, "feat(core): landed mid-release", "NOTES.md", "landed\n"), 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	// A GitHub release too, because the commit its body names is the other
	// thing the recovery has to get right.
	cfg.Scripts["publish"] = models.Script{`echo "DISPAT_EXPORT_GITHUB=" >> "$DISPAT_OUTPUT"`}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), Owner: "acme", Repo: "mono",
		APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

	res := r.Release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.HasCode(res.Events, "W242"),
		"the pull during the release is reported: %v", res.Events)
	assert.Contains(t, res.Stdout, "pulled the branch during the release")

	// The invariant, stated about the release commit itself rather than about
	// HEAD's parent: a recovery may merge more than once, and every merge
	// after the first has the previous merge as its first parent. What must
	// never move is the tag.
	release := releaseCommit(t, r, "chore(release): core@0.1.0")
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.Equal(t, release, strings.TrimSpace(r.Git("rev-list", "-n", "1", "core@0.1.0")),
		"the tag was written on the release commit and stayed there")
	assert.Contains(t, r.Git("ls-remote", "origin"), "refs/tags/core@0.1.0",
		"the tag reached the remote on the second attempt")

	// The branch tip is a merge, and the release commit is on its first-parent
	// chain: still on the branch, still the commit it was, never rewritten.
	parents := strings.Fields(r.Git("rev-list", "--parents", "-n", "1", "HEAD"))
	require.Len(t, parents, 3, "the tip joins two commits; log:\n%s", r.Git("log", "--format=%h %s", "-5"))
	assert.Contains(t, firstParents(t, r), release,
		"the release commit is reachable from the tip by first parents alone")

	// What arrived is on the branch too, and outside the release.
	assert.Contains(t, r.Git("log", "--format=%s"), "landed mid-release")
	require.Error(t, exec.Command("git", "-C", r.Root, "merge-base", "--is-ancestor",
		parents[2], release).Run(), "the foreign commit is not inside the release's ancestry")

	changelog, err := os.ReadFile(r.Path("packages", "core", "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(changelog), "first")
	assert.NotContains(t, string(changelog), "landed mid-release",
		"a commit that arrived after the plan was computed is not in this release's record")

	// The GitHub release names the release commit. It is created after the
	// push, when HEAD is already the merge, and the commit it means is the one
	// the tag is on rather than whatever the branch has moved to since.
	posted := bodies()
	require.Len(t, posted, 1, "one release was published")
	var created struct {
		Body            string `json:"body"`
		TargetCommitish string `json:"target_commitish"`
	}
	require.NoError(t, json.Unmarshal(posted[0], &created))
	assert.Contains(t, created.Body, "- commit: "+release)
	assert.Equal(t, release, created.TargetCommitish)
	assert.NotContains(t, created.Body, strings.TrimSpace(r.Git("rev-parse", "HEAD")),
		"the merge the branch ends on is not what the release records")

	// The run after the recovery is the point of the shape. It sees the window
	// the tag opens: the commit that arrived, the release commit's merge, and
	// nothing else. The merge is a chore(release), which the release scope
	// exempts, so the only thing in there with a package to name is the
	// foreign feature, and it releases.
	next := r.Release()
	require.Equal(t, 0, next.Code, "stdout:\n%s\nstderr:\n%s", next.Stdout, next.Stderr)
	assert.True(t, r.HasTag("core@0.2.0"), "what landed during the release is released next; tags: %v", r.TagList())
	assert.False(t, harness.HasCode(next.Events, "W131"),
		"neither the release commit nor the merge resolves to nothing noisily: %v", next.Events)
	after, err := os.ReadFile(r.Path("packages", "core", "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(after), "landed mid-release", "and it is recorded where it belongs")
}

// TestReleaseMergesWhatLandedTwice: the window the recovery recovers from is
// still open while it recovers. A commit landing between the merge and the
// retry push is the same surprise again, and answering it with a failed
// release would be the outcome the whole recovery exists to prevent, so the
// merge and the push go round again.
//
// The invariant holds across the rounds, which is the point of the test: every
// merge after the first has the previous merge as its first parent, and the
// tag still names the release commit underneath them all.
func TestReleaseMergesWhatLandedTwice(t *testing.T) {
	r := harness.New(t)
	bare := r.AddBareRemote()
	cfg := libsConfig(midReleasePush(t, bare, "feat(core): landed mid-release", "NOTES.md", "landed\n"), 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	landAgainDuringTheRecovery(t, bare, "feat(core): landed during the recovery")

	res := r.Release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.HasCode(res.Events, "W242"), "events: %v", res.Events)

	// Two merges, and the release commit is under both of them: neither round
	// moved the tag onto a merge, and neither rewrote it.
	release := releaseCommit(t, r, "chore(release): core@0.1.0")
	assert.Equal(t, release, strings.TrimSpace(r.Git("rev-list", "-n", "1", "core@0.1.0")),
		"the tag still names the release commit after two merges")
	assert.Contains(t, firstParents(t, r), release,
		"which is still on the tip's first-parent chain")
	log := r.Git("log", "--format=%s")
	assert.Contains(t, log, "landed during the recovery", "what arrived mid-recovery is on the branch")
	assert.Equal(t, 2, strings.Count(log, "chore(release): merge origin/"+harness.DefaultBranch),
		"one merge per round; log:\n%s", r.Git("log", "--format=%h %s", "-8"))
	assert.Contains(t, r.Git("ls-remote", "origin"), "refs/tags/core@0.1.0")
}

// TestReleaseRefusesToRepublishAnExistingTag: the recovery pushes this run's
// tags again, and commit.force is on by default, so a checkout whose tags are
// stale enough to have re-planned an already published version would
// force-move that published tag onto its own commit. The push this recovers
// from never reached a tag ref at all, so nothing used to stand between the
// two; now the remote's tags are read first and the run stops.
func TestReleaseRefusesToRepublishAnExistingTag(t *testing.T) {
	r := harness.New(t)
	bare := r.AddBareRemote()
	cfg := libsConfig(midReleasePush(t, bare, "chore: landed mid-release", "NOTES.md", "landed\n"), 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

	// Somebody else already published this version. The tag is on the remote
	// and this checkout has never seen it, which is exactly what makes the
	// plan compute it again.
	published := bareGit(t, bare, "rev-parse", harness.DefaultBranch)
	bareGit(t, bare, "-c", "user.email=other@dispat.test", "-c", "user.name=other clone",
		"tag", "-a", "core@0.1.0", "-m", "released elsewhere", strings.TrimSpace(published))

	res := r.Release()
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "already carries core@0.1.0")
	assert.Contains(t, res.Stdout, "Pull and run again")
	assert.Equal(t, strings.TrimSpace(published),
		strings.TrimSpace(bareGit(t, bare, "rev-list", "-n", "1", "core@0.1.0")),
		"the published tag is where its own release left it")
}

// TestReleaseSettlesAConflictAndKeepsBothSides: what landed changed the same
// content the release did. The release has already published by then, so
// stopping would leave its commit and tags nowhere but this clone; it
// completes instead, and hands the conflict over rather than deciding it.
//
// This release's side wins every conflicting file, because that is the tree
// the tag names. Everything of theirs that did not conflict is in the merge
// all the same. Their side is pushed to a branch of its own so nothing is
// lost, and both records name the files and that branch.
func TestReleaseSettlesAConflictAndKeepsBothSides(t *testing.T) {
	r := harness.New(t)
	bare := r.AddBareRemote()
	srv, bodies := githubFake(t)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	// Their commit rewrites the changelog this release is about to write, and
	// adds a file nothing here touches.
	script := midReleasePushes(t, bare,
		foreignChange{"docs(core): a changelog of their own", "packages/core/CHANGELOG.md",
			"# Changelog\n\nwritten by somebody else\n"},
		foreignChange{"fix(core): something of their own", "THEIRS.md", "theirs"})
	cfg := libsConfig(script, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	cfg.Scripts["publish"] = models.Script{`echo "DISPAT_EXPORT_GITHUB=" >> "$DISPAT_OUTPUT"`}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), Owner: "acme", Repo: "mono",
		APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")
	r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

	res := releaseLocked(r)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.HasCode(res.Events, "W243"), "the conflict is reported: %v", res.Events)

	// The tag is where it always is, on the commit the run planned.
	release := releaseCommit(t, r, "chore(release): core@0.1.0")
	assert.Equal(t, release, strings.TrimSpace(r.Git("rev-list", "-n", "1", "core@0.1.0")))
	assert.Contains(t, firstParents(t, r), release)
	assert.Contains(t, r.Git("ls-remote", "origin"), "refs/tags/core@0.1.0")

	// This side of the conflict is what the branch carries, with no markers in
	// it, and everything of theirs that did not conflict is here too.
	changelog := readFile(t, r, "packages", "core", "CHANGELOG.md")
	assert.NotContains(t, changelog, "written by somebody else", "their side did not overwrite the release")
	assert.NotContains(t, changelog, "<<<<", "and no conflict markers were committed")
	assert.Contains(t, changelog, "## core@0.1.0 (", "the entry is still the one a re-run recognises")
	assert.FileExists(t, r.Path("THEIRS.md"), "what of theirs did not conflict is in the merge")

	// Their side is kept, at the tip they pushed.
	quarantine := conflictBranchOf(t, r)
	assert.Contains(t, quarantine, "release-conflicts/core-0.1.0-", "branch: %s", quarantine)
	assert.Contains(t, r.Git("log", "--format=%s", "origin/"+quarantine),
		"a changelog of their own", "the branch holds the side that was set aside")

	// Both records name the files and the branch.
	assert.Contains(t, changelog, "packages/core/CHANGELOG.md")
	assert.Contains(t, changelog, quarantine)
	posted := bodies()
	require.Len(t, posted, 1)
	var created struct {
		Body string `json:"body"`
	}
	require.NoError(t, json.Unmarshal(posted[0], &created))
	assert.Contains(t, created.Body, "packages/core/CHANGELOG.md")
	assert.Contains(t, created.Body, quarantine)

	assertLockCleared(t, r, bare)
	assert.NoFileExists(t, r.Path(".git", "MERGE_HEAD"))

	// And the run after it plans normally: the merge and the release commit
	// both name no package, and what arrived releases on its own terms.
	next := r.Release()
	require.Equal(t, 0, next.Code, "stdout:\n%s\nstderr:\n%s", next.Stdout, next.Stderr)
	assert.False(t, harness.HasCode(next.Events, "W131"),
		"the merge carries a changelog edit and still resolves to no package: %v", next.Events)
	assert.True(t, r.HasTag("core@0.1.1"), "tags: %v", r.TagList())
}

// conflictBranchOf is the quarantine branch the run pushed, read off the
// remote rather than guessed at: its name carries a timestamp.
func conflictBranchOf(t *testing.T, r *harness.Repo) string {
	t.Helper()
	r.Git("fetch", "-q", "origin")
	for _, line := range strings.Split(r.Git("ls-remote", "--heads", "origin"), "\n") {
		_, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && strings.HasPrefix(ref, "refs/heads/release-conflicts/") {
			return strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	t.Fatalf("no release-conflicts branch on the remote:\n%s", r.Git("ls-remote", "--heads", "origin"))
	return ""
}
