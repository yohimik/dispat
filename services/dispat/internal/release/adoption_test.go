// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// exitStatus is a script's non-zero exit, as the shell runner reports one.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitStatus) ExitCode() int { return int(e) }

// answeringRunner answers each command by its first word: an exit status, and
// what the command prints.
type answeringRunner struct {
	mu      sync.Mutex
	ran     []string
	exits   map[string]error
	stdouts map[string]string
	stderrs map[string]string
}

func (r *answeringRunner) Run(_ context.Context, dir, command string, _ []string, stdout, stderr io.Writer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	word, _, _ := strings.Cut(command, " ")
	r.ran = append(r.ran, word+" "+filepath.Base(dir))
	_, _ = io.WriteString(stdout, r.stdouts[word])
	_, _ = io.WriteString(stderr, r.stderrs[word])
	return r.exits[word]
}

func (r *answeringRunner) count(word string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, ran := range r.ran {
		if strings.HasPrefix(ran, word+" ") {
			n++
		}
	}
	return n
}

// adoptionPlan is one or more independent packages whose publish is refused,
// in folders of their own, verified by `verify` unless the policy says
// otherwise.
func adoptionPlan(t *testing.T, policy model.AdoptPublished, names ...string) *plan.Plan {
	t.Helper()
	p := mkPlan(planSpec{Names: names})
	root := t.TempDir()
	for _, name := range names {
		space := *p.Releases[name].Pkg.Space
		space.AdoptPublished = policy
		dir := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		p.Releases[name].Pkg = &model.Package{Name: name, Dir: dir, Space: &space}
	}
	return p
}

var verifyPolicy = model.AdoptPublished{VerifyScript: []string{"verify"}}

func TestAdoptionVerdicts(t *testing.T) {
	refused := exitStatus(1)
	for _, c := range []struct {
		name     string
		verify   error
		policy   model.AdoptPublished
		status   Status
		code     string
		verified int
	}{
		{name: "a matching artefact is adopted", verify: nil, policy: verifyPolicy, status: StatusPublished, verified: 1},
		{name: "a version the destination does not hold stays a failure",
			verify: exitStatus(verifyAbsentExit), policy: verifyPolicy, status: StatusFailed, verified: 1},
		{name: "another artefact is refused", verify: refused, policy: verifyPolicy,
			status: StatusFailed, code: plan.CodeAdoptionRefused, verified: 1},
		{name: "adoption off asks nothing", verify: nil,
			policy: model.AdoptPublished{IsDisabled: true, VerifyScript: []string{"verify"}}, status: StatusFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := &answeringRunner{exits: map[string]error{"publish": errors.New("E409 already exists"), "verify": c.verify}}
			tg := &fakeTagger{}
			res := (&Executor{BuildConcurrency: 1, PublishConcurrency: 1, Runner: r, Tagger: tg, Log: zerolog.Nop()}).
				Run(context.Background(), adoptionPlan(t, c.policy, "core"))
			assert.Equal(t, c.status, res["core"].Status, "%v", res["core"].Err)
			assert.Equal(t, c.verified, r.count("verify"))
			assert.Equal(t, c.status == StatusPublished, len(tg.tags) == 1, "the tag follows an adoption alone")
			if c.code != "" {
				require.Error(t, res["core"].Err)
				assert.Contains(t, res["core"].Err.Error(), c.code)
			}
		})
	}
}

// TestAdoptionRefusalStopsLaterPublications holds §19.4's "raise E198 and
// stop": a publication queued behind the refused one never starts, and is
// cancelled rather than failed so the next run owes it the same release.
func TestAdoptionRefusalStopsLaterPublications(t *testing.T) {
	r := &answeringRunner{exits: map[string]error{"publish": errors.New("E409 already exists"), "verify": exitStatus(1)}}
	e := &Executor{BuildConcurrency: 2, PublishConcurrency: 1, Runner: r, Log: zerolog.Nop(),
		PublishGroup: func(*plan.Release) string { return "one lane" }}
	res := e.Run(context.Background(), adoptionPlan(t, verifyPolicy, "a", "b"))
	assert.Equal(t, StatusFailed, res["a"].Status)
	assert.Equal(t, StatusCancelled, res["b"].Status)
	assert.Equal(t, 1, r.count("publish"), "b's publish never started")
}

func TestAdoptionThroughNpmIntegrity(t *testing.T) {
	for _, c := range []struct {
		name, integrity, stdout, stderr string
		npmErr                          error
		status                          Status
		isAsked                         bool
	}{
		{name: "match", integrity: "sha512-abc", stdout: "sha512-abc\n", status: StatusPublished, isAsked: true},
		{name: "quoted match", integrity: "sha512-abc", stdout: "'sha512-abc'\n", status: StatusPublished, isAsked: true},
		{name: "mismatch", integrity: "sha512-abc", stdout: "sha512-xyz\n", status: StatusFailed, isAsked: true},
		{name: "missing dist.integrity", integrity: "sha512-abc", stdout: "", status: StatusFailed, isAsked: true},
		{name: "unknown package", integrity: "sha512-abc", npmErr: exitStatus(1), stderr: "npm ERR! code E404",
			status: StatusFailed, isAsked: true},
		{name: "registry unreachable", integrity: "sha512-abc", npmErr: exitStatus(1), stderr: "ETIMEDOUT",
			status: StatusFailed, isAsked: true},
		{name: "no integrity exported", status: StatusFailed},
		{name: "not an integrity", integrity: "deadbeef", status: StatusFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := &answeringRunner{exits: map[string]error{"publish": errors.New("EPUBLISHCONFLICT"), "npm": c.npmErr},
				stdouts: map[string]string{"npm": c.stdout}, stderrs: map[string]string{"npm": c.stderr}}
			p := adoptionPlan(t, model.AdoptPublished{}, "core")
			rel := p.Releases["core"]
			require.NoError(t, os.WriteFile(filepath.Join(rel.Pkg.Dir, "package.json"), []byte(`{"name":"@acme/core"}`), 0o644))
			if c.integrity != "" {
				rel.Outputs = []plan.Output{{Name: IntegrityOutput, Value: c.integrity}}
			}
			res := (&Executor{BuildConcurrency: 1, PublishConcurrency: 1, Runner: r, Log: zerolog.Nop()}).
				Run(context.Background(), p)
			assert.Equal(t, c.status, res["core"].Status, "%v", res["core"].Err)
			assert.Equal(t, c.isAsked, r.count("npm") == 1)
			isRefused := res["core"].Err != nil && strings.Contains(res["core"].Err.Error(), plan.CodeAdoptionRefused)
			assert.Equal(t, c.name == "mismatch" || c.name == "missing dist.integrity", isRefused)
		})
	}
}

func TestNpmIntegrityNeedsAPackageName(t *testing.T) {
	r := &answeringRunner{exits: map[string]error{"publish": errors.New("EPUBLISHCONFLICT")}}
	p := adoptionPlan(t, model.AdoptPublished{}, "core")
	p.Releases["core"].Outputs = []plan.Output{{Name: IntegrityOutput, Value: "sha512-abc"}}
	require.NoError(t, os.WriteFile(filepath.Join(p.Releases["core"].Pkg.Dir, "package.json"), []byte(`{}`), 0o644))
	res := (&Executor{BuildConcurrency: 1, PublishConcurrency: 1, Runner: r, Log: zerolog.Nop()}).Run(context.Background(), p)
	assert.Equal(t, StatusFailed, res["core"].Status)
	assert.Zero(t, r.count("npm"), "a folder that names no npm package is not asked about")
}

func TestOutcomeUnknownIsNeverAdopted(t *testing.T) {
	assert.True(t, isOutcomeUnknown(fmt.Errorf("wrapped: %w", &diagnosticError{code: "E228"})))
	assert.False(t, isOutcomeUnknown(errors.New("plain")))
}

type diagnosticError struct{ code string }

func (e *diagnosticError) Error() string          { return e.code }
func (e *diagnosticError) DiagnosticCode() string { return e.code }

func TestQuoteShell(t *testing.T) {
	assert.Equal(t, `'@acme/core@1.0.0'`, quoteShell("@acme/core@1.0.0"))
	assert.Equal(t, `'it'\''s'`, quoteShell("it's"))
}

// durableFunc is a DurableRecorderx made of a function.
type durableFunc func(context.Context, *plan.Release) error

func (f durableFunc) RecordDurably(ctx context.Context, rel *plan.Release) error { return f(ctx, rel) }

func TestDurableRecordFollowsTheTag(t *testing.T) {
	for _, c := range []struct {
		name   string
		err    error
		code   string
		tagger Taggerx
		calls  int
	}{
		{name: "pushed", tagger: &fakeTagger{}, calls: 1},
		{name: "not pushed", err: fmt.Errorf("%w: refused", ErrRecordNotPushed), code: plan.CodePushFailed,
			tagger: &fakeTagger{}, calls: 1},
		{name: "held elsewhere", err: fmt.Errorf("%w: elsewhere", ErrTagAtOtherCommit), code: plan.CodeTagAtOtherCommit,
			tagger: &fakeTagger{}, calls: 1},
		{name: "a receipt stands in for a deferred tag", calls: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			e := &Executor{BuildConcurrency: 1, PublishConcurrency: 1, Runner: &answeringRunner{}, Log: zerolog.Nop(),
				Tagger: c.tagger,
				Durable: durableFunc(func(context.Context, *plan.Release) error {
					calls++
					return c.err
				})}
			if c.tagger == nil {
				e.Tagger = nil
			}
			res := e.Run(context.Background(), adoptionPlan(t, model.AdoptPublished{}, "core"))
			assert.Equal(t, StatusPublished, res["core"].Status)
			assert.Equal(t, c.calls, calls)
			if c.code == "" {
				assert.Empty(t, res["core"].Critical)
				return
			}
			require.Len(t, res["core"].Critical, 1)
			assert.Contains(t, res["core"].Critical[0].Error(), c.code)
		})
	}
}

// failingTagger refuses every write, with no tag of the name anywhere.
type failingTagger struct{ fakeTagger }

func (f *failingTagger) CreateTag(context.Context, string, string, string) error {
	return errors.New("tag refused")
}

func TestDurableRecordReportsTagWriteFailure(t *testing.T) {
	calls := 0
	tagger := &failingTagger{}
	e := &Executor{BuildConcurrency: 1, PublishConcurrency: 1, Runner: &answeringRunner{}, Log: zerolog.Nop(),
		Tagger: tagger,
		Durable: durableFunc(func(ctx context.Context, rel *plan.Release) error {
			calls++
			return CreateReleaseTag(ctx, tagger, rel, false, zerolog.Nop())
		})}
	res := e.Run(context.Background(), adoptionPlan(t, model.AdoptPublished{}, "core"))
	assert.Equal(t, StatusPublished, res["core"].Status)
	assert.Equal(t, 1, calls, "the durable recorder owns tag creation after the receipt")
	require.Len(t, res["core"].Critical, 1)
	assert.Contains(t, res["core"].Critical[0].Error(), plan.CodeTagFailed)
}

func TestRecordPendingPublishesNothing(t *testing.T) {
	r := &answeringRunner{}
	tg := &fakeTagger{}
	recorded := 0
	e := &Executor{BuildConcurrency: 1, PublishConcurrency: 1, Runner: r, Tagger: tg, Log: zerolog.Nop(),
		PendingRecord: func(rel *plan.Release) (PendingRecord, bool) {
			return PendingRecord{Integrity: "sha512-core", ExportedCommit: "abc123"}, rel.Pkg.Name == "core"
		},
		Durable: durableFunc(func(ctx context.Context, rel *plan.Release) error {
			recorded++
			return CreateReleaseTag(ctx, tg, rel, false, zerolog.Nop())
		})}
	p := adoptionPlan(t, model.AdoptPublished{}, "core", "ui")
	p.Releases["core"].Outputs = append(p.Releases["core"].Outputs,
		plan.Output{Name: IntegrityOutput, Value: "sha512-core"})
	res := e.Run(context.Background(), p)
	assert.Equal(t, StatusPublished, res["core"].Status)
	assert.Equal(t, StatusPublished, res["ui"].Status)
	assert.Equal(t, 1, r.count("publish"), "only ui publishes")
	assert.ElementsMatch(t, []string{"core@1.0.1", "ui@1.0.1"}, tg.tags)
	assert.Equal(t, 2, recorded)
	assert.Equal(t, "abc123", p.Releases["core"].ExportedCommit(), "the publish script's tag target survives a receipt retry")
}
