// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Who a process says it is, decided from the three things that can say so:
// the environment it was started in, the command it is running, and the
// configuration it read.
//
// The order matters more than any single row. A checkout that travelled to a
// worker carries the orchestrator's configuration, so a nested dispat inside
// a task would call itself an orchestrator if the file were asked first.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	public "github.com/yohimik/dispat/pkg/models"
)

func TestResolveSender(t *testing.T) {
	host, err := os.Hostname()
	require.NoError(t, err, "the machine running the tests can say what it is called")
	workerAuthority := FormatWorkerAuthorityEnv("build-a")
	for name, tc := range map[string]struct {
		settings       *public.ExecutionConfig
		isServingTasks bool
		env            []string
		wantRole       string
		wantNode       string
	}{
		"a repository that states no execution object": {},
		"an orchestrator that names itself": {
			settings: &public.ExecutionConfig{Name: "ci-1"},
			wantRole: public.ExecutionRoleOrchestrator, wantNode: "ci-1"},
		"an orchestrator that names no node": {
			settings: &public.ExecutionConfig{Role: public.ExecutionRoleOrchestrator},
			wantRole: public.ExecutionRoleOrchestrator, wantNode: host},
		"a node whose file calls it a worker": {
			settings: &public.ExecutionConfig{Role: public.ExecutionRoleWorker, Name: "build-b"},
			wantRole: public.ExecutionRoleWorker, wantNode: "build-b"},
		"a node serving tasks": {
			settings: &public.ExecutionConfig{Name: "build-a"}, isServingTasks: true,
			wantRole: public.ExecutionRoleWorker, wantNode: "build-a"},
		"a node serving tasks with nothing configured yet": {
			isServingTasks: true,
			wantRole:       public.ExecutionRoleWorker, wantNode: host},
		"a task's nested command, whatever the checkout says": {
			settings: &public.ExecutionConfig{Name: "ci-1",
				Workers: []public.ExecutionWorkerConfig{{Name: "build-a"}}},
			env:      workerAuthority,
			wantRole: public.ExecutionRoleWorker, wantNode: "build-a"},
		"a task's nested command that was told no node": {
			env:      []string{AuthorityEnv + "=" + WorkerAuthority},
			wantRole: public.ExecutionRoleWorker, wantNode: host},
		"a serving command under somebody else's authority": {
			settings: &public.ExecutionConfig{Name: "build-b"}, isServingTasks: true,
			env:      workerAuthority,
			wantRole: public.ExecutionRoleWorker, wantNode: "build-a"},
	} {
		t.Run(name, func(t *testing.T) {
			sender := ResolveSender(tc.settings, tc.isServingTasks, tc.env)

			assert.Equal(t, tc.wantRole, sender.Role)
			assert.Equal(t, tc.wantNode, sender.Node)
			assert.Equal(t, tc.wantRole != "", sender.IsStated())
		})
	}
}

// TestAnUnnamedNodeIsNeverAnonymous: a name is what the first question of any
// log reading is answered with, so the one case where nothing can be resolved
// still answers something.
func TestAnUnnamedNodeIsNeverAnonymous(t *testing.T) {
	assert.NotEmpty(t, resolveNodeName(""))
	assert.Equal(t, "build-a", resolveNodeName("build-a"))
}
