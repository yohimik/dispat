package app

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

type cancelSuccessfulPublishRunner struct{ cancel context.CancelFunc }

func (r cancelSuccessfulPublishRunner) Run(context.Context, string, string, []string, io.Writer, io.Writer) error {
	r.cancel()
	return nil
}

type interruptionHookRunner struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	cancelOn string
	ran      []string
}

func (r *interruptionHookRunner) Run(_ context.Context, _, command string, _ []string, _, _ io.Writer) error {
	r.mu.Lock()
	r.ran = append(r.ran, command)
	cancel := command == r.cancelOn && r.cancel != nil
	r.mu.Unlock()
	if cancel {
		r.cancel()
	}
	return nil
}

func (r *interruptionHookRunner) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ran...)
}

func interruptedWorkspaceRecordFixture(t *testing.T) (*workspaceRecorder, *plan.Plan, string, string) {
	t.Helper()
	w, rel := recordFixture(t, true, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	sourceRemote := addRecordBareRemote(t, source.repo.Root, "publish-source")
	controlRemote := addRecordBareRemote(t, control.repo.Root, "publish-control")
	for _, record := range w.ordered {
		record.repo.Commit.Push = true
		record.repo.Commit.Remote = "publish-control"
		record.branch = "main"
		record.expectedHead = recordGit(t, record.repo.Root, "rev-parse", "HEAD")
	}
	source.repo.Commit.Remote = "publish-source"
	rel.Pkg.Changelog.Enabled = true
	rel.Pkg.Space.PublishScript = []string{"publish"}
	pl := &plan.Plan{
		Order:           []string{rel.Pkg.Name},
		Releases:        map[string]*plan.Release{rel.Pkg.Name: rel},
		Providers:       map[string][]string{},
		RepositoryHeads: map[string]string{"source": source.expectedHead, config.ControlRepository: control.expectedHead},
	}
	return w, pl, sourceRemote, controlRemote
}

func installInterruptionHooks(w *workspaceRecorder, sourceRunner, controlRunner *interruptionHookRunner) {
	install := func(record *repositoryRecord, runner *interruptionHookRunner, prefix string) {
		record.repo.Config.Scripts = map[string]config.Script{
			prefix + "-before-commit": {prefix + "-before-commit"},
			prefix + "-after-commit":  {prefix + "-after-commit"},
			prefix + "-post-commit":   {prefix + "-post-commit"},
			prefix + "-before-push":   {prefix + "-before-push"},
			prefix + "-after-push":    {prefix + "-after-push"},
		}
		record.repo.Config.Run.BeforeCommit = []string{prefix + "-before-commit"}
		record.repo.Config.Run.AfterCommit = []string{prefix + "-after-commit"}
		record.repo.Config.Run.PostCommit = []string{prefix + "-post-commit"}
		record.repo.Config.Run.BeforePush = []string{prefix + "-before-push"}
		record.repo.Config.Run.AfterPush = []string{prefix + "-after-push"}
		record.hooks.runner = runner
	}
	install(w.byName["source"], sourceRunner, "source")
	install(w.byName[config.ControlRepository], controlRunner, "control")
}

func assertInterruptedWorkspaceRecordsPersist(t *testing.T, w *workspaceRecorder, pl *plan.Plan, sourceRemote, controlRemote string) {
	t.Helper()
	rel := pl.Releases["lib"]
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	sourceHead := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	controlHead := recordGit(t, control.repo.Root, "rev-parse", "HEAD")
	assert.Equal(t, sourceHead, recordGit(t, source.repo.Root, "rev-parse", rel.TagName()+"^{commit}"))
	assert.Equal(t, sourceHead, recordGit(t, sourceRemote, "rev-parse", rel.TagName()+"^{commit}"))
	assert.Equal(t, sourceHead, recordGit(t, control.repo.Root, "rev-parse", "HEAD:source"))
	assert.Equal(t, controlHead, recordGit(t, controlRemote, "rev-parse", "refs/heads/main"))
}

func TestInterruptedWorkspaceRecordingSkipsCommitAndPushHooks(t *testing.T) {
	t.Run("cancelled before durable record tail", func(t *testing.T) {
		w, pl, sourceRemote, controlRemote := interruptedWorkspaceRecordFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		sourceHooks, controlHooks := &interruptionHookRunner{}, &interruptionHookRunner{}
		installInterruptionHooks(w, sourceHooks, controlHooks)

		results := (&release.Executor{
			BuildConcurrency: 1, PublishConcurrency: 1,
			Runner: cancelSuccessfulPublishRunner{cancel: cancel}, Recorders: []release.ReleaseRecorder{w},
			BlockOnRecordFailure: true, AcquirePublish: w.acquirePublish,
		}).Run(ctx, pl)

		require.Equal(t, release.StatusPublished, results["lib"].Status)
		assert.Empty(t, results["lib"].Critical)
		assert.ErrorIs(t, ctx.Err(), context.Canceled)
		assert.Empty(t, sourceHooks.commands(), "source commit/push observers must not start after interruption")
		assert.Empty(t, controlHooks.commands(), "control commit/push observers must not start after interruption")
		assertInterruptedWorkspaceRecordsPersist(t, w, pl, sourceRemote, controlRemote)
	})

	t.Run("cancelled during recording before next hook", func(t *testing.T) {
		w, pl, sourceRemote, controlRemote := interruptedWorkspaceRecordFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		sourceHooks := &interruptionHookRunner{cancel: cancel, cancelOn: "source-before-commit"}
		controlHooks := &interruptionHookRunner{}
		installInterruptionHooks(w, sourceHooks, controlHooks)

		results := (&release.Executor{
			BuildConcurrency: 1, PublishConcurrency: 1,
			Runner: &pinCaptureRunner{}, Recorders: []release.ReleaseRecorder{w},
			BlockOnRecordFailure: true, AcquirePublish: w.acquirePublish,
		}).Run(ctx, pl)

		require.Equal(t, release.StatusPublished, results["lib"].Status)
		assert.Empty(t, results["lib"].Critical)
		assert.ErrorIs(t, ctx.Err(), context.Canceled)
		assert.Equal(t, []string{"source-before-commit"}, sourceHooks.commands(),
			"cancellation during one observer must suppress every later observer")
		assert.Empty(t, controlHooks.commands(), "control observers start after the cancellation")
		assertInterruptedWorkspaceRecordsPersist(t, w, pl, sourceRemote, controlRemote)
	})
}
