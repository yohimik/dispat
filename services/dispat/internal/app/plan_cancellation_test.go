package app

import (
	"context"
	"errors"
	"testing"
)

func TestCancelledPlanDoesNotStartConfigurationDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A cancelled invocation must not inspect a workspace or resolve remote
	// record destinations. No configuration is needed to honor cancellation.
	_, err := (&App{}).planOptions(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
}
