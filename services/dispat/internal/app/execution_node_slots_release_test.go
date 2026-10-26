package app

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
	public "github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// Two same-package stages are graph-ready together after build. One fails
// while the other waits for the only durable node slot. The failed stage must
// settle and release its slot without waiting for that queued sibling.
func TestLocalReleaseFailureReleasesSlotBeforeParallelSiblingSettlement(t *testing.T) {
	space := &model.Space{Name: "libs", BuildScript: []string{"build"},
		TestScript: []string{"check"}, OnFailScript: []string{"on-fail"},
		UserStages: []model.UserStage{{Name: "lint", Commands: []string{"lint"}}}}
	rel := &plan.Release{Pkg: &model.Package{Name: "a", Dir: "a", Space: space,
		BuildWeight: 1, PublishWeight: 1},
		Current: ccme.Version{Major: 1}, Next: ccme.Version{Major: 1, Patch: 1},
		Bump: ccme.BumpPatch, NewWork: true}
	pl := &plan.Plan{Order: []string{"a"}, Releases: map[string]*plan.Release{"a": rel}}
	started := make(chan struct{})
	gate := make(chan struct{})
	second := make(chan struct{}, 1)
	var mu sync.Mutex
	firstCommand := ""
	onFail := 0
	runner := scriptRunnerFunc(func(ctx context.Context, _, command string, _ []string, _, _ io.Writer) error {
		switch command {
		case "check", "lint":
			mu.Lock()
			if firstCommand == "" {
				firstCommand = command
				close(started)
				mu.Unlock()
				select {
				case <-gate:
					return errors.New("stage failed")
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			mu.Unlock()
			second <- struct{}{}
		case "on-fail":
			mu.Lock()
			onFail++
			mu.Unlock()
		}
		return nil
	})
	settings := &config.File{Execution: &public.ExecutionConfig{Concurrency: public.Int(1)}}
	a := New(t.TempDir(), settings, zerolog.Nop())
	slots, err := execution.OpenNodeSlotAuthority(filepath.Join(t.TempDir(), "node-state"), "same-node", 1)
	require.NoError(t, err)
	a.localNodeSlots, a.localSlotRunID = slots, "release-with-parallel-stages"
	executor := &release.Executor{BuildConcurrency: 3, PublishConcurrency: 1,
		Runner: runner, Log: zerolog.Nop()}
	compiled, err := executor.CompileTaskGraph(pl)
	require.NoError(t, err)
	require.NoError(t, a.installExplicitLocalReleaseSlots(executor, compiled))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		results, runErr := executor.RunCompiled(ctx, pl, compiled)
		if runErr == nil && results["a"].Status != release.StatusFailed {
			runErr = errors.New("failing sibling did not fail the package")
		}
		done <- runErr
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("neither parallel stage started")
	}
	select {
	case <-second:
		t.Fatal("parallel sibling ran while the first stage held the sole slot")
	case <-time.After(150 * time.Millisecond):
	}
	close(gate)
	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-ctx.Done():
		t.Fatal("release deadlocked while a same-package sibling waited for the slot")
	}
	mu.Lock()
	require.Equal(t, 1, onFail)
	mu.Unlock()
	held, err := slots.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held)
}
