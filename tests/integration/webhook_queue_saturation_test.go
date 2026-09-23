// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// A blocked endpoint cannot turn a large release into a blocked publisher.
// Thirty-two independent packages emit more than the endpoint's 128 queued
// stage and package events even while its first delivery remains in flight.
func TestWebhookFullQueueDropsDeliveriesWithoutRetryingPublication(t *testing.T) {
	unblock := make(chan struct{})
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-unblock:
		case <-req.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(unblock)
		srv.Close()
	})

	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig("echo built", models.WebhookConfig{URL: srv.URL, Timeout: 60}))
	names := packageNames(32, "pkg")
	seedIndependentPackages(r, names)

	begin := time.Now()
	res := r.Release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	select {
	case <-started:
	default:
		t.Fatal("the blocked webhook endpoint received no request")
	}
	assert.Contains(t, res.Stdout+res.Stderr, "webhook delivery dropped, queue is full")
	assert.True(t, harness.IsCodePresent(res.Events, "W239"), "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout+res.Stderr, "abandoned")
	assert.Less(t, time.Since(begin), 90*time.Second, "the webhook flush must be bounded")

	tags := r.TagList()
	require.Len(t, tags, len(names), "every planned package publishes exactly one tag: %v", tags)
	for _, name := range names {
		assert.True(t, r.IsTagged(name+"@0.1.0"), "missing %s; tags: %v", name, tags)
	}
}
