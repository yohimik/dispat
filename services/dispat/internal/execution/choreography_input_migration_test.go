// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// These are the crafted-input refusals of the retired worker-push fixture,
// exercised through the same signed v3 carrier and input reader used by a
// serving node. Each case reaches the real Git object store and signer.
func TestChoreographyWorkerRefusesCraftedCarriedInputs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(*OutputManifest, *AssignmentInput, *Result)
		body     []byte
		kind     MessageKind
		secret   string
		unsigned bool
		want     OutputReason
	}{
		{name: "no manifest digest", change: func(_ *OutputManifest, in *AssignmentInput, _ *Result) {
			in.Digest = ""
		}, want: ReasonOutputDigest},
		{name: "object without result", kind: MessageReservation, want: ReasonOutputIdentity},
		{name: "result without outputs", change: func(_ *OutputManifest, _ *AssignmentInput, r *Result) {
			r.Outputs = nil
		}, want: ReasonBytesMissing},
		{name: "result without signature", unsigned: true, want: ReasonOutputIdentity},
		{name: "result signed by foreign key", secret: "different signer", want: ReasonOutputIdentity},
		{name: "unreadable result JSON", body: []byte("not a result"), want: ReasonOutputIdentity},
		{name: "different admitted manifest digest", change: func(_ *OutputManifest, in *AssignmentInput, _ *Result) {
			in.Digest = "not-the-admitted-digest"
		}, want: ReasonOutputDigest},
		{name: "foreign generation", change: func(m *OutputManifest, _ *AssignmentInput, _ *Result) {
			m.Generation = "another-generation"
		}, want: ReasonOutputIdentity},
		{name: "tree omits declared file", change: func(m *OutputManifest, _ *AssignmentInput, _ *Result) {
			m.Entries = append(m.Entries, ManifestEntry{Path: "dist/missing.js", Type: EntryFile,
				Mode: EntryModeFile, Size: 1, SHA256: digestOf("x")})
			m.Files++
			m.Bytes++
		}, want: ReasonTreeMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
			outputs := newOutputFixture(t, "packages/core")
			outputs.write(t, "dist/lib.js", "one\n", 0o644)
			manifest, err := outputs.capture(t, []string{"dist"}, testLimits)
			require.NoError(t, err)
			manifest.Run, manifest.PlanDigest = binding.Run, binding.PlanDigest
			manifest.Generation, manifest.Task = binding.Generation, "provider:build"
			require.NoError(t, fixture.git.ImportObjectFrom(t.Context(), outputs.dir,
				manifest.OutputTree, 1<<20))
			input := AssignmentInput{Task: "provider:build", Package: "provider",
				Path: "providers/lib", Digest: manifest.Digest}
			result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
				Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
				PlanDigest: binding.PlanDigest, Task: "provider:build", Attempt: 1,
				Node: "node-a"}, Status: StatusSucceeded, Outputs: manifest}
			if tc.change != nil {
				tc.change(manifest, &input, &result)
			}
			manifest.Digest = CalculateManifestDigest(manifest)
			if tc.name != "different admitted manifest digest" && tc.name != "no manifest digest" {
				input.Digest = manifest.Digest
			}
			body := tc.body
			if body == nil {
				body, err = json.Marshal(result)
				require.NoError(t, err)
			}
			kind := tc.kind
			if kind == "" {
				kind = MessageResult
			}
			writer := fixture.mailbox
			if tc.secret != "" {
				foreignSigner, signErr := NewSigner(tc.secret)
				require.NoError(t, signErr)
				writer = NewGitMailbox(fixture.endpoint, fixture.git, foreignSigner, zerolog.Nop())
			}
			resultOID := ""
			if tc.unsigned {
				plumbing := gitx.NewPlumbing(fixture.git)
				documentOID := plumbing.HashObject(t.Context(), strings.NewReader(string(body)))
				inner := plumbing.MakeTree(t.Context(), []gitx.TreeEntry{{Mode: gitx.TreeModeFile,
					Type: "blob", OID: documentOID, Name: string(MessageResult) + ".json"}})
				tree := plumbing.MakeTree(t.Context(), []gitx.TreeEntry{{Mode: gitx.TreeModeDir,
					Type: "tree", OID: inner, Name: messageDir}})
				resultOID = plumbing.CommitTree(t.Context(), tree, nil, string(MessageResult))
				require.NoError(t, plumbing.Err())
			} else {
				resultOID, err = writer.commitMessage(t.Context(), kind, body, nil, nil)
			}
			require.NoError(t, err)
			input.Commit = resultOID
			assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion,
				Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
				PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
				Node: "node-a", Branch: spec.Branch,
				IssuedAt: time.Now().UTC().Format(time.RFC3339)},
				GraphDigest: binding.GraphDigest, BootstrapOID: persist.opts.BootstrapOID,
				Weight: 1, Inputs: []AssignmentInput{input}, Limits: testLimits}
			assignmentOID, err := fixture.mailbox.PrepareAssignmentOnReservation(t.Context(),
				&assignment, spec.ReservationOID)
			require.NoError(t, err)
			view, err := fixture.mailbox.OpenCarriedAssignment(t.Context(), assignmentOID, 1<<20)
			require.NoError(t, err)
			worker := &Worker{Mailbox: fixture.mailbox, Cache: fixture.git}
			_, _, reason, err := worker.readInputManifest(t.Context(), view, assignment, input)
			require.Error(t, err)
			require.Equal(t, tc.want, reason, "%v", err)
			require.Equal(t, spec.ReservationOID, remoteChoreographyOID(t, fixture, spec.Branch),
				"a refused input cannot claim or run its consumer")
		})
	}
}

func TestChoreographyWorkerRefusesMissingDirectInputCarrier(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	resultOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult, []byte(`{}`), nil, nil)
	require.NoError(t, err)
	assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
		Node: "node-a", Branch: spec.Branch},
		GraphDigest: binding.GraphDigest, BootstrapOID: persist.opts.BootstrapOID,
		Weight: 1, Inputs: []AssignmentInput{{Task: "provider:build", Commit: resultOID, Digest: "digest"}}}
	body, err := json.Marshal(assignment)
	require.NoError(t, err)
	missing, err := fixture.mailbox.commitMessage(t.Context(), MessageAssignment,
		body, []string{spec.ReservationOID}, nil)
	require.NoError(t, err)
	_, err = fixture.mailbox.OpenCarriedAssignment(t.Context(), missing, 1<<20)
	require.Equal(t, ReasonChain, RejectionReason(err))
	require.Equal(t, spec.ReservationOID, remoteChoreographyOID(t, fixture, spec.Branch))
}

func TestChoreographyAssignmentDeduplicatesSharedInputCarrier(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	resultOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult, []byte(`{}`), nil, nil)
	require.NoError(t, err)
	assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
		Node: "node-a", Branch: spec.Branch},
		GraphDigest: binding.GraphDigest, BootstrapOID: persist.opts.BootstrapOID,
		Weight: 1, Inputs: []AssignmentInput{
			{Task: "provider:build", Package: "provider", Path: "providers/first", Commit: resultOID, Digest: "digest"},
			{Task: "provider:build", Package: "provider", Path: "providers/second", Commit: resultOID, Digest: "digest"}}}
	assignmentOID, err := fixture.mailbox.PrepareAssignmentOnReservation(t.Context(), &assignment, spec.ReservationOID)
	require.NoError(t, err)
	parents, err := fixture.git.CommitParents(t.Context(), assignmentOID)
	require.NoError(t, err)
	require.Equal(t, []string{spec.ReservationOID, resultOID}, parents)
	_, err = fixture.mailbox.OpenCarriedAssignment(t.Context(), assignmentOID, 1<<20)
	require.NoError(t, err)
}

func TestChoreographyWorkerInstallsOneVerifiedResultAtTwoInputPaths(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	outputs := newOutputFixture(t, "packages/core")
	outputs.write(t, "dist/lib.js", "shared\n", 0o644)
	manifest, err := outputs.capture(t, []string{"dist"}, testLimits)
	require.NoError(t, err)
	manifest.Run, manifest.PlanDigest = binding.Run, binding.PlanDigest
	manifest.Generation, manifest.Task = binding.Generation, "provider:build"
	manifest.Digest = CalculateManifestDigest(manifest)
	require.NoError(t, fixture.git.ImportObjectFrom(t.Context(), outputs.dir,
		manifest.OutputTree, 1<<20))
	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: "provider:build", Attempt: 1,
		Node: "node-a"}, Status: StatusSucceeded, Outputs: manifest}
	body, err := json.Marshal(result)
	require.NoError(t, err)
	resultOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult, body, nil, nil)
	require.NoError(t, err)
	assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
		Node: "node-a", Branch: spec.Branch},
		GraphDigest: binding.GraphDigest, BootstrapOID: persist.opts.BootstrapOID,
		Weight: 1, Limits: testLimits, Inputs: []AssignmentInput{
			{Task: "provider:build", Package: "provider", Path: "providers/first", Commit: resultOID, Digest: manifest.Digest},
			{Task: "provider:build", Package: "provider", Path: "providers/second", Commit: resultOID, Digest: manifest.Digest}}}
	assignmentOID, err := fixture.mailbox.PrepareAssignmentOnReservation(t.Context(), &assignment, spec.ReservationOID)
	require.NoError(t, err)
	parents, err := fixture.git.CommitParents(t.Context(), assignmentOID)
	require.NoError(t, err)
	require.Equal(t, []string{spec.ReservationOID, resultOID}, parents)
	root := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, os.MkdirAll(root, 0o755))
	worker := &Worker{Mailbox: fixture.mailbox, Cache: fixture.git}
	checkout := NewCheckout(fixture.git, root, zerolog.Nop())
	installed, _, reason, err := worker.installTaskInputs(t.Context(), assignmentOID,
		assignment, checkout, t.TempDir(), zerolog.Nop())
	require.NoError(t, err, "input refusal: %s", reason)
	require.Len(t, installed, 2)
	for _, owner := range []string{"first", "second"} {
		body, err := os.ReadFile(filepath.Join(root, "providers", owner, "dist", "lib.js"))
		require.NoError(t, err)
		require.Equal(t, "shared\n", string(body))
	}
}
