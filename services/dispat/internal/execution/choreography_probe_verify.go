// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
)

// ReadVerifiedPinnedProbe authenticates the signed v3 probe, its ordered
// direct-parent source/bootstrap carriers, and the retained bootstrap. A
// choreographer must establish this binding before using a return endpoint
// or independently interpreting pinned inputs.
func (m *GitMailbox) ReadVerifiedPinnedProbe(ctx context.Context, oid string,
	maxDocumentBytes, maxTagBytes int64) (*Assignment, RunBootstrap, error) {
	if m == nil || oid == "" || maxDocumentBytes <= 0 || maxTagBytes <= 0 {
		return nil, RunBootstrap{}, fmt.Errorf("execution: incomplete pinned probe read")
	}
	tree, err := m.readTree(ctx, oid)
	if err != nil {
		return nil, RunBootstrap{}, err
	}
	if tree.kind != MessageAssignment {
		return nil, RunBootstrap{}, &Rejection{Reason: ReasonChain}
	}
	document, err := m.Read(ctx, ChainTip{OID: oid, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, maxDocumentBytes)
	if err != nil {
		return nil, RunBootstrap{}, err
	}
	var probe Assignment
	if err := json.Unmarshal(document, &probe); err != nil ||
		probe.Protocol != ChoreographyProtocolVersion || probe.Kind != KindProbe ||
		probe.BootstrapOID == "" || probe.PinnedInputs == nil ||
		probe.GraphDigest == "" || probe.Run == "" || probe.Generation == "" ||
		probe.PlanDigest == "" || probe.Node == "" || probe.Branch == "" {
		return nil, RunBootstrap{}, &Rejection{Reason: ReasonUnreadable}
	}
	parents := make([]string, 0, len(probe.PinnedInputs.Repositories)+1)
	seen := map[string]bool{}
	for _, source := range probe.PinnedInputs.Repositories {
		if source.Snapshot == "" || seen[source.Snapshot] || source.Snapshot == probe.BootstrapOID {
			return nil, RunBootstrap{}, &Rejection{Reason: ReasonChain}
		}
		seen[source.Snapshot] = true
		parents = append(parents, source.Snapshot)
	}
	parents = append(parents, probe.BootstrapOID)
	actualParents, err := m.plumbing.CommitParents(ctx, oid)
	if err != nil {
		return nil, RunBootstrap{}, err
	}
	if !slices.Equal(parents, actualParents) {
		return nil, RunBootstrap{}, &Rejection{Reason: ReasonChain}
	}
	bootstrap, err := m.ReadChoreographyBootstrap(ctx, probe.BootstrapOID,
		maxDocumentBytes, maxTagBytes)
	if err != nil {
		return nil, RunBootstrap{}, err
	}
	if bootstrap.Run != probe.Run || bootstrap.Generation != probe.Generation ||
		bootstrap.PlanDigest != probe.PlanDigest || bootstrap.GraphDigest != probe.GraphDigest ||
		bootstrap.Mode != probe.DistributionMode ||
		bootstrap.ReturnEndpoint != probe.ReturnEndpoint ||
		bootstrap.ReturnCertSHA256 != probe.ReturnCertSHA256 ||
		!reflect.DeepEqual(bootstrap.PinnedInputs, *probe.PinnedInputs) {
		return nil, RunBootstrap{}, &Rejection{Reason: ReasonChain}
	}
	return &probe, bootstrap, nil
}
