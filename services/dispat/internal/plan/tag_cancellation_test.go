// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

type blockingTagGitx struct {
	*fakeGit
	started chan struct{}
	calls   atomic.Int32
}

func (g *blockingTagGitx) TagsForPackages(ctx context.Context, _ map[string]gitx.TagFormat) (map[string]gitx.Tags, error) {
	g.calls.Add(1)
	g.started <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCancellationUnblocksBulkTagInventory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pkgs, history := groupWorkspace(1000, model.VersioningFixed)
	git := &blockingTagGitx{fakeGit: history, started: make(chan struct{}, len(pkgs))}
	done := make(chan error, 1)
	go func() {
		_, err := Compute(ctx, git, Options{Packages: pkgs})
		done <- err
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	select {
	case <-git.started:
	case <-deadline.C:
		t.Fatal("tag inventory did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled planning did not finish")
	}
	if calls := git.calls.Load(); calls != 1 {
		t.Fatalf("read %d tag inventories, want one", calls)
	}
}
