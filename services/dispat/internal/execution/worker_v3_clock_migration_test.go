// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The v3 worker and first node use this exact acceptance rule for signed
// probe/task headers. A modest peer clock difference cannot lose an otherwise
// authenticated claim; a timestamp beyond the replay window still refuses.
func TestWorkerV3AcceptsModestClockSkew(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	binding := Binding{Node: "build-a", Branch: "dispat-worker-build-a-20261026-probe-abc"}
	header := Header{Protocol: ChoreographyProtocolVersion, Kind: KindProbe,
		Run: "run-1", Task: PreflightTask, Attempt: 1,
		Node: binding.Node, Branch: binding.Branch}
	for _, row := range []struct {
		name string
		skew time.Duration
		want RejectReason
	}{
		{"ahead fifteen minutes", 15 * time.Minute, ""},
		{"behind fifteen minutes", -15 * time.Minute, ""},
		{"ahead beyond window", replayWindow + time.Second, ReasonIssuedAt},
		{"behind beyond window", -replayWindow - time.Second, ReasonIssuedAt},
	} {
		t.Run(row.name, func(t *testing.T) {
			message := header
			message.IssuedAt = now.Add(row.skew).Format(time.RFC3339)
			require.Equal(t, row.want, CheckHeaderVersion(HeaderVersionCheck{
				Header: message, Binding: binding, Now: now, Version: ChoreographyProtocolVersion}))
		})
	}
}
