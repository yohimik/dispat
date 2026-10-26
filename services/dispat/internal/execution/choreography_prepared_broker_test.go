// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type preparedBrokerPolicy struct{}

func (preparedBrokerPolicy) ValidateOutcome(string, string) error { return nil }
func (preparedBrokerPolicy) ApplyOutcome(string, string) error    { return nil }
func (preparedBrokerPolicy) ShouldSkip(string) (bool, string, error) {
	return false, "", nil
}

func TestBrokerPreparedFailureBlockerIsArrivalIndependent(t *testing.T) {
	for _, order := range [][]string{{"alpha", "zeta"}, {"zeta", "alpha"}} {
		specs := []ChoreographyTaskSpec{
			{Task: "alpha", Package: "alpha", Kind: string(KindPrepare),
				Budget: "build", Weight: 1, EligibleNodes: []string{"node-a"}},
			{Task: "zeta", Package: "zeta", Kind: string(KindPrepare),
				Budget: "build", Weight: 1, EligibleNodes: []string{"node-a"}},
			{Task: "relay", Virtual: true, Predecessors: []string{"alpha", "zeta"}},
			{Task: "consumer", Package: "app", Kind: string(KindBuild),
				Budget: "build", Weight: 1, EligibleNodes: []string{"node-a"},
				Predecessors: []string{"relay"}},
		}
		_, persist, _, _ := newChoreographyLedgerFixture(t, specs...)
		opts := choreographyFixtureBrokerOptions(persist)
		opts.OutcomePolicy = preparedBrokerPolicy{}
		broker, err := RecoverFirstNodeBroker(t.Context(), opts)
		require.NoError(t, err)
		for _, name := range order {
			parent := broker.tasks[name]
			parent.terminal = true
			broker.discharge(parent)
		}
		require.True(t, broker.tasks["relay"].terminal)
		require.Equal(t, "alpha", broker.tasks["consumer"].preparedBlockedBy)
	}
}
