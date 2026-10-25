// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// A package's record reaches the remote the instant the package publishes
// (CCME §19.1, §19.4), and a publication whose record was lost anyway is
// reconciled with the destination rather than failing every later run.
//
// The registry these scenarios publish to is a file outside every checkout:
// one line per accepted version, carrying the artefact identity the build
// exported, and an upload of a version it already holds is refused the way a
// real registry refuses it. A killed run is a publish script sending SIGKILL
// to dispat after its own upload landed, so the run stops in the one window no
// record can close: the publication exists and dispat never heard.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// fileRegistry is the scenarios' publication destination and the scripts
// that talk to it.
type fileRegistry struct {
	path string
	// salt is a file whose content goes into every artefact identity, so a
	// scenario rebuilds a different artefact by changing it.
	salt string
	// kill names the package whose publish kills dispat once its own upload
	// has landed, while the marker exists.
	kill string
}

func newFileRegistry(t *testing.T) fileRegistry {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the file registry's scripts are POSIX shell")
	}
	dir := t.TempDir()
	reg := fileRegistry{path: filepath.Join(dir, "registry"), salt: filepath.Join(dir, "salt"),
		kill: filepath.Join(dir, "kill")}
	require.NoError(t, os.WriteFile(reg.salt, []byte("first\n"), 0o644))
	return reg
}

// build exports the artefact identity a real build would read off its
// tarball.
func (f fileRegistry) build() string {
	return `printf 'INTEGRITY=sha512-%s-%s-%s\n' "$DISPAT_PACKAGE" "$DISPAT_NEW_VERSION" "$(cat ` +
		harness.ShQuote(f.salt) + `)" >> "$DISPAT_OUTPUT"`
}

// publish uploads, refuses a version the registry already holds, and kills
// the run after the named package's upload landed.
func (f fileRegistry) publish() string {
	reg, kill := harness.ShQuote(f.path), harness.ShQuote(f.kill)
	return `entry="$DISPAT_PACKAGE@$DISPAT_NEW_VERSION"; ` +
		`if grep -q "^$entry " ` + reg + ` 2>/dev/null; then echo "E409 $entry already exists" >&2; exit 1; fi; ` +
		`printf '%s %s\n' "$entry" "$DISPAT_OUTPUT_INTEGRITY" >> ` + reg + `; ` +
		`if [ -f ` + kill + ` ] && [ "$DISPAT_PACKAGE" = "$(cat ` + kill + `)" ]; then rm -f ` + kill + `; kill -9 $PPID; fi`
}

// verify answers adoptPublished's question: 3 when the registry does not hold
// the version, 0 when it holds this run's artefact, 1 otherwise.
func (f fileRegistry) verify() string {
	return `line=$(grep "^$DISPAT_PACKAGE@$DISPAT_NEW_VERSION " ` + harness.ShQuote(f.path) + `) || exit 3; ` +
		`[ "${line#* }" = "$DISPAT_OUTPUT_INTEGRITY" ]`
}

// uploads is how many times the registry accepted one version.
func (f fileRegistry) uploads(t *testing.T, entry string) int {
	t.Helper()
	data, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return strings.Count(string(data), entry+" ")
}

// killDuring arms the kill for one package's publish.
func (f fileRegistry) killDuring(t *testing.T, pkg string) {
	t.Helper()
	require.NoError(t, os.WriteFile(f.kill, []byte(pkg), 0o644))
}

// durabilityRepo is core and its consumer ui publishing to the file registry,
// under the given commit policy, with adoption verified by the registry's own
// script.
func durabilityRepo(t *testing.T, reg fileRegistry, commit *models.CommitConfig) (*harness.Repo, string) {
	t.Helper()
	r := harness.New(t)
	remote := r.AddBareRemote()
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {reg.build()}, "publish": {reg.publish()}, "verify": {reg.verify()}}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()}}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "ui", Provider: "core"}}
	cfg.Commit = commit
	cfg.Publish = &models.PublishConfig{AdoptPublished: &models.AdoptPublishedConfig{Verify: []string{"verify"}}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "ui")
	r.Commit("feat(core,ui): first release")
	r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	return r, remote
}

// remoteRefs is every ref the remote advertises, peeled lines left out.
func remoteRefs(t *testing.T, r *harness.Repo, remote string) map[string]string {
	t.Helper()
	refs := map[string]string{}
	for _, line := range strings.Split(r.Git("ls-remote", remote), "\n") {
		object, name, ok := strings.Cut(line, "\t")
		if ok && !strings.HasSuffix(name, "^{}") {
			refs[name] = object
		}
	}
	return refs
}

func receiptsOn(refs map[string]string) []string {
	var receipts []string
	for name := range refs {
		if strings.HasPrefix(name, "refs/dispat/receipts/") {
			receipts = append(receipts, strings.TrimPrefix(name, "refs/dispat/receipts/"))
		}
	}
	return receipts
}

// TestKilledReleaseLeavesEachPublicationOnTheRemote is the guarantee in one
// scenario. A release-commit run is killed after core published and recorded,
// and while ui's upload had landed and dispat had not heard of it. The remote
// holds core's receipt; it cannot hold ui's. The retry, from a fresh clone the
// way CI makes one, publishes neither again: core's record is finished from its
// receipt (W246), ui's upload is met at the registry and adopted on its
// identity (W196), and finalize consumes the receipt once both tags are on the
// remote.
func TestKilledReleaseLeavesEachPublicationOnTheRemote(t *testing.T) {
	reg := newFileRegistry(t)
	r, remote := durabilityRepo(t, reg, &models.CommitConfig{Enabled: models.Bool(true), Push: true})
	head := r.Git("rev-parse", "HEAD")

	reg.killDuring(t, "ui")
	killed := r.Release()
	require.NotZero(t, killed.Code, "stdout:\n%s\nstderr:\n%s", killed.Stdout, killed.Stderr)

	refs := remoteRefs(t, r, remote)
	assert.Equal(t, []string{"core@0.1.0"}, receiptsOn(refs), "core's publication is on the remote")
	assert.Equal(t, head, r.Git("rev-parse", "refs/dispat/receipts/core@0.1.0^{}"),
		"the receipt names the commit the release was planned on")
	assert.Equal(t, 1, reg.uploads(t, "core@0.1.0"))
	assert.Equal(t, 1, reg.uploads(t, "ui@0.1.0"), "the kill came after ui's upload landed")

	ci := harness.Clone(t, remote)
	retry := ci.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.True(t, harness.IsCodePresentForPackage(retry.Events, "W246", "core"), "stdout:\n%s", retry.Stdout)
	assert.True(t, harness.IsCodePresentForPackage(retry.Events, "W196", "ui"), "stdout:\n%s", retry.Stdout)
	assert.Equal(t, 1, reg.uploads(t, "core@0.1.0"), "the retry publishes nothing again")
	assert.Equal(t, 1, reg.uploads(t, "ui@0.1.0"))

	refs = remoteRefs(t, ci, remote)
	assert.Contains(t, refs, "refs/tags/core@0.1.0")
	assert.Contains(t, refs, "refs/tags/ui@0.1.0")
	assert.Empty(t, receiptsOn(refs), "finalize consumed the receipt once the tags were on the remote")
	assert.Empty(t, ci.Git("for-each-ref", "refs/dispat/"))
	assert.Equal(t, "chore(release): core@0.1.0, ui@0.1.0", ci.Git("log", "-1", "--format=%s", "core@0.1.0"))

	done := ci.Status("--require-release")
	assert.Equal(t, 3, done.Code, "stdout:\n%s", done.Stdout)
}

// TestKilledTagOnlyReleasePushedEachTag is the same kill with no release
// commit. With push on, each release tag is pushed the moment it is written,
// so core's record is on the remote before ui's publish runs, and the retry
// from the same checkout releases ui alone, by adoption.
func TestKilledTagOnlyReleasePushedEachTag(t *testing.T) {
	reg := newFileRegistry(t)
	r, remote := durabilityRepo(t, reg, &models.CommitConfig{Push: true})
	head := r.Git("rev-parse", "HEAD")

	reg.killDuring(t, "ui")
	killed := r.Release()
	require.NotZero(t, killed.Code, "stdout:\n%s\nstderr:\n%s", killed.Stdout, killed.Stderr)
	refs := remoteRefs(t, r, remote)
	assert.Contains(t, refs, "refs/tags/core@0.1.0")
	assert.NotContains(t, refs, "refs/tags/ui@0.1.0")
	assert.Equal(t, head, r.Git("ls-remote", remote, "refs/tags/core@0.1.0^{}")[:40])
	assert.Empty(t, receiptsOn(refs), "a run with no release commit needs no receipt")

	retry := r.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.True(t, harness.IsCodePresentForPackage(retry.Events, "W196", "ui"), "stdout:\n%s", retry.Stdout)
	assert.Equal(t, 1, reg.uploads(t, "core@0.1.0"), "core is recorded and never published again")
	assert.Equal(t, 1, reg.uploads(t, "ui@0.1.0"))
	assert.Contains(t, remoteRefs(t, r, remote), "refs/tags/ui@0.1.0")
}

// TestRejectedDurableRecordBlocksConsumers preserves the provider's published
// outcome while refusing a consumer until a later run can prove and record it.
func TestRejectedDurableRecordBlocksConsumers(t *testing.T) {
	for _, test := range []struct {
		name, rejectedRef string
		commit            *models.CommitConfig
	}{
		{"receipt", "refs/dispat/receipts/*", &models.CommitConfig{Enabled: models.Bool(true), Push: true}},
		{"tag", "refs/tags/core@0.1.0", &models.CommitConfig{Push: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reg := newFileRegistry(t)
			r, remote := durabilityRepo(t, reg, test.commit)
			hook := "#!/bin/sh\ncase \"$1\" in " + test.rejectedRef + ") exit 1 ;; esac\n"
			hookPath := filepath.Join(remote, "hooks", "update")
			require.NoError(t, os.WriteFile(hookPath, []byte(hook), 0o755))

			first := r.Release()
			require.NotZero(t, first.Code, "stdout:\n%s\nstderr:\n%s", first.Stdout, first.Stderr)
			assert.Equal(t, 1, reg.uploads(t, "core@0.1.0"))
			assert.Equal(t, 0, reg.uploads(t, "ui@0.1.0"), "consumer cannot publish without a durable provider record")
			assert.NotContains(t, remoteRefs(t, r, remote), "refs/tags/core@0.1.0")
			assert.True(t, harness.IsCodePresentForPackage(first.Events, "W194", "ui"), "stdout:\n%s", first.Stdout)

			require.NoError(t, os.Remove(hookPath))
			retry := harness.Clone(t, remote)
			result := retry.Release()
			require.Zero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
			code := "W196"
			if test.name == "tag" {
				code = "W246"
			}
			assert.True(t, harness.IsCodePresentForPackage(result.Events, code, "core"), "stdout:\n%s", result.Stdout)
			assert.Equal(t, 1, reg.uploads(t, "core@0.1.0"))
			assert.Equal(t, 1, reg.uploads(t, "ui@0.1.0"))
		})
	}
}

func TestTagOnlyRejectedPushSettlesFromSameCheckout(t *testing.T) {
	reg := newFileRegistry(t)
	r, remote := durabilityRepo(t, reg, &models.CommitConfig{Push: true})
	hookPath := filepath.Join(remote, "hooks", "update")
	require.NoError(t, os.WriteFile(hookPath,
		[]byte("#!/bin/sh\ncase \"$1\" in refs/tags/core@0.1.0) exit 1 ;; esac\n"), 0o755))
	first := r.Release()
	require.NotZero(t, first.Code)
	assert.Equal(t, 1, reg.uploads(t, "core@0.1.0"))
	assert.Contains(t, remoteRefs(t, r, remote), "refs/dispat/receipts/core@0.1.0")
	assert.NotContains(t, remoteRefs(t, r, remote), "refs/tags/core@0.1.0")
	require.NoError(t, os.Remove(hookPath))
	retry := r.Release()
	require.Zero(t, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, 1, reg.uploads(t, "core@0.1.0"))
	assert.Equal(t, 1, reg.uploads(t, "ui@0.1.0"))
	refs := remoteRefs(t, r, remote)
	assert.Contains(t, refs, "refs/tags/core@0.1.0")
	assert.NotContains(t, refs, "refs/dispat/receipts/core@0.1.0")
}

func TestUnreadableRemoteReceiptRefusesLocalTagRetry(t *testing.T) {
	reg := newFileRegistry(t)
	r, remote := durabilityRepo(t, reg, &models.CommitConfig{Push: true})
	hookPath := filepath.Join(remote, "hooks", "update")
	require.NoError(t, os.WriteFile(hookPath,
		[]byte("#!/bin/sh\ncase \"$1\" in refs/tags/core@0.1.0) exit 1 ;; esac\n"), 0o755))
	require.NotZero(t, r.Release().Code)
	require.NoError(t, os.Remove(hookPath))
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*fetch --prune --no-tags*"})
	retry := r.CommandEnv(fault.Env())
	require.NotZero(t, retry.Code, "stdout:\n%s", retry.Stdout)
	assert.Equal(t, 1, fault.Matches())
	assert.Contains(t, retry.Stdout, "publication receipts could not be read")
	assert.Equal(t, 1, reg.uploads(t, "core@0.1.0"))
	assert.Equal(t, 0, reg.uploads(t, "ui@0.1.0"))
	refs := remoteRefs(t, r, remote)
	assert.Contains(t, refs, "refs/dispat/receipts/core@0.1.0")
	assert.NotContains(t, refs, "refs/tags/core@0.1.0")
}

// TestReceiptRetryRefusesADifferentArtefact keeps the old receipt when a
// same-head retry's new build cannot prove the earlier upload's identity.
func TestReceiptRetryRefusesADifferentArtefact(t *testing.T) {
	reg := newFileRegistry(t)
	r, remote := durabilityRepo(t, reg, &models.CommitConfig{Enabled: models.Bool(true), Push: true})
	reg.killDuring(t, "ui")
	killed := r.Release()
	require.NotZero(t, killed.Code)
	require.Equal(t, []string{"core@0.1.0"}, receiptsOn(remoteRefs(t, r, remote)))
	require.NoError(t, os.WriteFile(reg.salt, []byte("different\n"), 0o644))

	retry := harness.Clone(t, remote)
	result := retry.Release()
	require.NotZero(t, result.Code, "stdout:\n%s", result.Stdout)
	assert.True(t, harness.IsCodePresentForPackage(result.Events, "E198", "core"), "stdout:\n%s", result.Stdout)
	assert.Equal(t, []string{"core@0.1.0"}, receiptsOn(remoteRefs(t, retry, remote)),
		"the receipt remains until a matching identity can be recorded")
	assert.NotContains(t, remoteRefs(t, retry, remote), "refs/tags/core@0.1.0")
	assert.Equal(t, 1, reg.uploads(t, "core@0.1.0"))
	assert.Equal(t, 1, reg.uploads(t, "ui@0.1.0"), "the retry starts no further publication")
}

// TestReceiptBehindTheHeadIsRecordedWhereItWasPlanned covers a retry that
// does not start where the stopped run did: a commit landed on the branch
// first. The receipt's release cannot be finished as the run's own, because
// the head now carries work it never published. Its tag is written at the
// receipt's commit instead, the plan is computed again from that record, and
// the new work releases as the next version.
func TestReceiptBehindTheHeadIsRecordedWhereItWasPlanned(t *testing.T) {
	reg := newFileRegistry(t)
	r, remote := durabilityRepo(t, reg, &models.CommitConfig{Enabled: models.Bool(true), Push: true})
	reg.killDuring(t, "core")
	killed := r.Release()
	require.NotZero(t, killed.Code, "stdout:\n%s\nstderr:\n%s", killed.Stdout, killed.Stderr)
	// The kill came after core's upload and before its receipt: the window
	// no record closes. ui never published.
	assert.Empty(t, receiptsOn(remoteRefs(t, r, remote)))

	ci := harness.Clone(t, remote)
	retry := ci.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.True(t, harness.IsCodePresentForPackage(retry.Events, "W196", "core"), "stdout:\n%s", retry.Stdout)

	// Now the receipt case proper: a second release killed after core's
	// receipt reached the remote, then a commit landing before the retry.
	ci.WriteFile("packages/core/second.txt", "second\n")
	ci.WriteFile("packages/ui/second.txt", "second\n")
	ci.Commit("feat(core,ui): second release")
	ci.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	second := ci.Git("rev-parse", "HEAD")
	reg.killDuring(t, "ui")
	killed = ci.Release()
	require.NotZero(t, killed.Code, "stdout:\n%s", killed.Stdout)
	require.Equal(t, []string{"core@0.2.0"}, receiptsOn(remoteRefs(t, ci, remote)))

	landed := harness.Clone(t, remote)
	landed.WriteFile("packages/core/third.txt", "third\n")
	landed.Commit("fix(core): landed before the retry")
	landed.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

	again := landed.Release()
	require.Equal(t, 0, again.Code, "stdout:\n%s\nstderr:\n%s", again.Stdout, again.Stderr)
	assert.True(t, harness.IsCodePresentForPackage(again.Events, "W246", "core"), "stdout:\n%s", again.Stdout)
	assert.Equal(t, second, landed.Git("rev-parse", "core@0.2.0^{}"),
		"the record sits at the commit the stopped run planned it on")
	assert.Equal(t, 1, reg.uploads(t, "core@0.2.0"), "the receipt's version is never published again")
	assert.Equal(t, 1, reg.uploads(t, "core@0.2.1"), "the commit that landed releases as the next version")
	assert.True(t, harness.IsCodePresentForPackage(again.Events, "W196", "ui"), "stdout:\n%s", again.Stdout)
	assert.Equal(t, 1, reg.uploads(t, "ui@0.2.0"), "ui's upload landed before the kill and is adopted")
	assert.Empty(t, receiptsOn(remoteRefs(t, landed, remote)))
}

// TestAdoptionRefusesAForeignArtefact is §19.4's third step. A record lost
// after publication meets the registry's refusal on the next run, and the
// artefact the registry holds is not the one this run built. The package
// fails with E198, its tag is not written, and the run starts no further
// publication; an independent package waiting to publish is cancelled rather
// than published. With adoptPublished: false the refusal is an ordinary
// publish failure and the verify script is never asked.
func TestAdoptionRefusesAForeignArtefact(t *testing.T) {
	reg := newFileRegistry(t)
	r := harness.New(t)
	asked := filepath.Join(t.TempDir(), "asked")
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{
		"build":   {reg.build()},
		"slow":    {"sleep 2", reg.build()},
		"publish": {reg.publish()},
		"verify":  {"touch " + harness.ShQuote(asked), reg.verify()},
	}
	cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()}}
	cfg.Publish = &models.PublishConfig{AdoptPublished: &models.AdoptPublishedConfig{Verify: []string{"verify"}}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")
	r.ReleaseOK()
	require.Equal(t, 1, reg.uploads(t, "core@0.1.0"))

	// The record is lost, and the checkout now builds another artefact.
	r.Git("tag", "-d", "core@0.1.0")
	require.NoError(t, os.WriteFile(reg.salt, []byte("second\n"), 0o644))
	r.SeedPackage("packages", "zeta")
	cfg.Packages = map[string]models.PackageConfig{"zeta": {Flow: &models.SpaceFlowConfig{Build: []string{"slow"}}}}
	r.WriteConfigModel(cfg)
	r.Commit("feat(zeta): an independent package")

	refused := r.Release()
	require.NotZero(t, refused.Code, "stdout:\n%s\nstderr:\n%s", refused.Stdout, refused.Stderr)
	assert.True(t, harness.IsCodePresentForPackage(refused.Events, "E198", "core"), "stdout:\n%s", refused.Stdout)
	assert.False(t, r.IsTagged("core@0.1.0"), "a foreign artefact is never enrolled")
	assert.Equal(t, 0, reg.uploads(t, "zeta@0.1.0"), "no publication starts after E198")
	assert.Contains(t, refused.Stdout, `"status":"cancelled"`)
	assert.FileExists(t, asked)

	// Off: the refusal is the publish failure it always was.
	require.NoError(t, os.Remove(asked))
	cfg.Publish = &models.PublishConfig{AdoptPublished: models.AdoptPublishedOf(false)}
	r.WriteConfigModel(cfg)
	r.Commit("chore: adoption off")
	off := r.Release()
	require.NotZero(t, off.Code, "stdout:\n%s", off.Stdout)
	assert.False(t, harness.IsCodePresent(off.Events, "E198"), "stdout:\n%s", off.Stdout)
	assert.False(t, harness.IsCodePresent(off.Events, "W196"), "stdout:\n%s", off.Stdout)
	assert.NoFileExists(t, asked)
	assert.Equal(t, 1, reg.uploads(t, "zeta@0.1.0"), "with adoption off nothing stops the independent package")
}

// TestAdoptionThroughTheNpmRegistryIntegrity is the built-in check. With no
// verify script and an exported Subresource Integrity value, dispat asks npm
// for the version's dist.integrity and compares the two. npm here is a stand
// in on PATH that answers what the registry would.
func TestAdoptionThroughTheNpmRegistryIntegrity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the npm stand-in is a POSIX shell script")
	}
	for _, c := range []struct {
		name, held, code string
		isTagged         bool
	}{
		{name: "a matching integrity is adopted", held: "sha512-built", code: "W196", isTagged: true},
		{name: "another integrity is refused", held: "sha512-other", code: "E198"},
		{name: "a version the registry does not hold stays a publish failure", held: ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin := t.TempDir()
			npm := "#!/bin/sh\n[ \"$1\" = view ] || exit 2\n[ \"$2\" = '@acme/core@0.1.0' ] || exit 2\nprintf '%s\\n' " +
				harness.ShQuote(c.held) + "\n"
			if c.held == "" {
				npm = "#!/bin/sh\n[ \"$1\" = view ] || exit 2\n" +
					"[ \"$2\" = '@acme/core@0.1.0' ] || exit 2\n" +
					"echo 'npm ERR! code E404' >&2\nexit 1\n"
			}
			require.NoError(t, os.WriteFile(filepath.Join(bin, "npm"), []byte(npm), 0o755))

			r := harness.New(t)
			cfg := harness.BaseFile(1)
			cfg.Scripts = map[string]models.Script{
				"build":   {`printf 'INTEGRITY=sha512-built\n' >> "$DISPAT_OUTPUT"`},
				"publish": {`echo "npm ERR! code EPUBLISHCONFLICT" >&2; exit 1`},
			}
			cfg.Spaces = map[string]models.SpaceConfig{"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()}}
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`+"\n")
			r.Commit("feat(core): first release")

			res := r.CommandEnv([]string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}, "release")
			assert.Equal(t, c.isTagged, r.IsTagged("core@0.1.0"), "stdout:\n%s", res.Stdout)
			if c.isTagged {
				assert.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			} else {
				assert.NotZero(t, res.Code)
			}
			for _, code := range []string{"W196", "E198"} {
				assert.Equal(t, code == c.code, harness.IsCodePresentForPackage(res.Events, code, "core"),
					"%s, stdout:\n%s", code, res.Stdout)
			}
		})
	}
}

// TestTagOnlyPushLeavesARemoteRecordAtAnotherCommit holds the per-package
// push to the rules finalize's push follows. The same tag at the same commit
// is the retry of a write whose answer was lost and passes silently; at
// another commit it is a published record left where it is, reported E221
// with the package still published.
func TestTagOnlyPushLeavesARemoteRecordAtAnotherCommit(t *testing.T) {
	reg := newFileRegistry(t)
	r, remote := durabilityRepo(t, reg, &models.CommitConfig{Push: true})
	other := harness.Clone(t, remote)
	other.CommitEmpty("chore: elsewhere")
	other.Git("tag", "-a", "-m", "release core@0.1.0", "core@0.1.0")
	other.Git("push", "-q", "origin", "refs/tags/core@0.1.0")

	res := r.Release("--log-format", "json")
	require.NotZero(t, res.Code, "stdout:\n%s", res.Stdout)
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "E221", "core"), "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, `"status":"published"`)
	assert.Equal(t, other.Git("rev-parse", "HEAD"), r.Git("ls-remote", remote, "refs/tags/core@0.1.0^{}")[:40],
		"the remote's record is left where it is")
}

// writeReceiptByHand writes a receipt object into a checkout the way a run
// does, and pushes it, for the scenarios that start from receipts a run left.
func writeReceiptByHand(t *testing.T, r *harness.Repo, pkg, version, commit string) {
	t.Helper()
	tag := pkg + "@" + version
	ident := r.Git("var", "GIT_COMMITTER_IDENT")
	object := "object " + commit + "\ntype commit\ntag " + tag + "\ntagger " + ident + "\n\n" +
		"dispat receipt v1\n\npackage: " + pkg + "\nversion: " + version + "\ntag: " + tag +
		"\nplanned-head: " + commit + "\n"
	cmd := exec.Command("git", "-C", r.Root, "mktag")
	cmd.Stdin = strings.NewReader(object)
	out, err := cmd.Output()
	require.NoError(t, err)
	ref := "refs/dispat/receipts/" + tag
	r.Git("update-ref", ref, strings.TrimSpace(string(out)))
	r.Git("push", "-q", "origin", ref+":"+ref)
}

// TestLeftoverReceiptsAreSettledUnderTheLock covers the receipts a run finds
// that are not a publication it finishes. One whose tag the checkout and the
// remote hold is left over from a run whose record finished, and is removed.
// In tag-only mode, one whose tag only the checkout holds is what a failed
// tag push leaves: the tag is pushed first and the receipt removed after it.
// One on a commit the
// head does not reach cannot affect the plan and is left where it is. The
// release itself proceeds as if none of them were there.
func TestLeftoverReceiptsAreSettledUnderTheLock(t *testing.T) {
	reg := newFileRegistry(t)
	r, remote := durabilityRepo(t, reg, &models.CommitConfig{Push: true})
	r.ReleaseOK()
	require.Empty(t, receiptsOn(remoteRefs(t, r, remote)), "a finished run leaves no receipt behind")

	writeReceiptByHand(t, r, "ui", "0.1.0", r.Git("rev-parse", "ui@0.1.0^{}"))
	writeReceiptByHand(t, r, "core", "0.1.0", r.Git("rev-parse", "core@0.1.0^{}"))
	r.Git("push", "-q", "origin", ":refs/tags/core@0.1.0")
	r.Git("checkout", "-q", "--orphan", "elsewhere")
	r.Git("commit", "-q", "--allow-empty", "-m", "chore: unrelated history")
	writeReceiptByHand(t, r, "core", "9.0.0", r.Git("rev-parse", "HEAD"))
	r.Git("checkout", "-q", harness.DefaultBranch)
	r.Git("branch", "-D", "elsewhere")

	r.WriteFile("packages/core/second.txt", "second\n")
	r.Commit("feat(core): second release")
	res := r.Release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	refs := remoteRefs(t, r, remote)
	assert.Equal(t, []string{"core@9.0.0"}, receiptsOn(refs), "the stale receipts are removed, the unreachable one left")
	assert.Contains(t, refs, "refs/tags/core@0.1.0", "the tag only this checkout held is on the remote again")
	assert.Contains(t, res.Stdout, "a receipt names a commit this checkout's head does not reach")
	assert.Equal(t, 1, reg.uploads(t, "core@0.2.0"))
	assert.Equal(t, 1, reg.uploads(t, "core@0.1.0"))
}

// A release-commit run cannot infer an unfinished atomic finalization from
// a local tag alone. The prepared target is the evidence binding its tag,
// branch and receipt; without that evidence it keeps the receipt and starts
// no new publication.
func TestUnpreparedCommitModeReceiptKeepsLocalTag(t *testing.T) {
	reg := newFileRegistry(t)
	r, remote := durabilityRepo(t, reg, &models.CommitConfig{Enabled: models.Bool(true), Push: true})
	r.ReleaseOK()
	writeReceiptByHand(t, r, "core", "0.1.0", r.Git("rev-parse", "core@0.1.0^{}"))
	r.WriteFile("packages/core/second.txt", "second\n")
	r.Commit("feat(core): second release")

	res := r.Release()
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "no prepared finalization matching its receipt")
	assert.Contains(t, remoteRefs(t, r, remote), "refs/dispat/receipts/core@0.1.0")
	assert.Equal(t, 1, reg.uploads(t, "core@0.1.0"), "the completed publication is not repeated")
	assert.Equal(t, 0, reg.uploads(t, "core@0.2.0"), "no new publication starts after ambiguous evidence")
}

func TestReceiptRefusesALocalTagAtAnotherCommit(t *testing.T) {
	reg := newFileRegistry(t)
	r, remote := durabilityRepo(t, reg, &models.CommitConfig{Enabled: models.Bool(true), Push: true})
	planned := r.Git("rev-parse", "HEAD")
	writeReceiptByHand(t, r, "core", "0.1.0", planned)
	r.CommitEmpty("chore: a different local tag target")
	r.Git("tag", "-a", "-m", "wrong target", "core@0.1.0")

	result := r.Release()
	require.NotZero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout+result.Stderr, "no prepared finalization matching its receipt")
	refs := remoteRefs(t, r, remote)
	assert.Contains(t, refs, "refs/dispat/receipts/core@0.1.0")
	assert.NotContains(t, refs, "refs/tags/core@0.1.0")
	assert.Zero(t, reg.uploads(t, "core@0.1.0"), "no publish stage starts during a conflicting settlement")
}

func TestReceiptRefusesARemoteTagAtAnotherCommit(t *testing.T) {
	reg := newFileRegistry(t)
	r, remote := durabilityRepo(t, reg, &models.CommitConfig{
		Enabled: models.Bool(true), Push: true, Verify: models.Bool(false),
	})
	planned := r.Git("rev-parse", "HEAD")
	writeReceiptByHand(t, r, "core", "0.1.0", planned)
	r.Git("tag", "-a", "-m", "local tag at receipt commit", "core@0.1.0")
	other := harness.Clone(t, remote)
	other.CommitEmpty("chore: foreign release target")
	other.Git("tag", "-a", "-m", "foreign target", "core@0.1.0")
	other.Git("push", "-q", "origin", "refs/tags/core@0.1.0")
	foreign := other.Git("rev-parse", "core@0.1.0^{}")

	result := r.Release()
	require.NotZero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Contains(t, result.Stdout+result.Stderr, "no prepared finalization matching its receipt")
	refs := remoteRefs(t, r, remote)
	assert.Contains(t, refs, "refs/dispat/receipts/core@0.1.0")
	assert.Equal(t, foreign, r.Git("ls-remote", remote, "refs/tags/core@0.1.0^{}")[:40])
	assert.Zero(t, reg.uploads(t, "core@0.1.0"))
}
