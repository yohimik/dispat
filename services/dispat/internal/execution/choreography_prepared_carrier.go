// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

const choreographyPreparedCarrierKind MessageKind = "choreography-prepared-input"
const invalidChoreographyPreparedCarrierError = "execution: signed prepared frame differs from its graph, task, predecessor or source objects"
const missingChoreographyPreparedCarrierError = "execution: signed prepared frame has no complete input identity"
const choreographyPreparedCarrierMissingIdentity = ""
const choreographyPreparedCarrierNoRepositories = 0
const choreographyPreparedCarrierNoDocumentBytes = 0

// ChoreographyPreparedCarrier freezes every effective Assignment input before
// FrameReady. Source snapshots and admitted result objects are direct commit
// parents, so the signed ledger's FrameReady parent retains their closure.
type ChoreographyPreparedCarrier struct {
	ChoreographyRunBinding
	GraphRootOID          string                   `json:"graphRootOid"`
	Task                  string                   `json:"task"`
	TemplateDigest        string                   `json:"templateDigest"`
	PredecessorResultOIDs []string                 `json:"predecessorResultOids"`
	Bindings              ChoreographyTaskBindings `json:"bindings"`
}

func (c ChoreographyPreparedCarrier) SourceSnapshot(repository string) string {
	for _, source := range c.Bindings.Repositories {
		if source.Name == repository {
			return source.Snapshot
		}
	}
	return choreographyPreparedCarrierMissingIdentity
}

func choreographyPreparedCarrierParents(c ChoreographyPreparedCarrier) ([]string, error) {
	if !c.ChoreographyRunBinding.valid() || c.GraphRootOID == choreographyPreparedCarrierMissingIdentity || c.Task == choreographyPreparedCarrierMissingIdentity ||
		c.TemplateDigest == choreographyPreparedCarrierMissingIdentity || c.Bindings.PreparedInputOID != choreographyPreparedCarrierMissingIdentity ||
		!slices.Equal(c.PredecessorResultOIDs, c.Bindings.PredecessorResultOIDs) ||
		len(c.Bindings.Repositories) == choreographyPreparedCarrierNoRepositories {
		return nil, fmt.Errorf(missingChoreographyPreparedCarrierError)
	}
	parents := []string{c.GraphRootOID}
	for _, source := range c.Bindings.Repositories {
		if source.Snapshot == choreographyPreparedCarrierMissingIdentity || source.Path == choreographyPreparedCarrierMissingIdentity {
			return nil, fmt.Errorf(missingChoreographyPreparedCarrierError)
		}
		if !slices.Contains(parents, source.Snapshot) {
			parents = append(parents, source.Snapshot)
		}
	}
	for _, input := range c.Bindings.Inputs {
		if input.Commit == choreographyPreparedCarrierMissingIdentity || input.Digest == choreographyPreparedCarrierMissingIdentity || input.Task == choreographyPreparedCarrierMissingIdentity {
			return nil, fmt.Errorf(missingChoreographyPreparedCarrierError)
		}
		if !slices.Contains(parents, input.Commit) {
			parents = append(parents, input.Commit)
		}
	}
	return parents, nil
}

func (m *GitMailbox) PrepareChoreographyPreparedCarrier(ctx context.Context,
	carrier ChoreographyPreparedCarrier, maxDocumentBytes int64) (string, error) {
	if m == nil || maxDocumentBytes <= choreographyPreparedCarrierNoDocumentBytes {
		return choreographyPreparedCarrierMissingIdentity, fmt.Errorf(missingChoreographyPreparedCarrierError)
	}
	parents, err := choreographyPreparedCarrierParents(carrier)
	if err != nil {
		return choreographyPreparedCarrierMissingIdentity, err
	}
	document, err := json.Marshal(carrier)
	if err != nil || int64(len(document)) > maxDocumentBytes {
		return choreographyPreparedCarrierMissingIdentity, fmt.Errorf(missingChoreographyPreparedCarrierError)
	}
	return m.commitMessage(ctx, choreographyPreparedCarrierKind, document, parents, nil)
}

func (m *GitMailbox) ReadChoreographyPreparedCarrier(ctx context.Context, oid string,
	maxDocumentBytes int64) (ChoreographyPreparedCarrier, error) {
	if m == nil || oid == choreographyPreparedCarrierMissingIdentity || maxDocumentBytes <= choreographyPreparedCarrierNoDocumentBytes {
		return ChoreographyPreparedCarrier{}, fmt.Errorf(missingChoreographyPreparedCarrierError)
	}
	tree, err := m.readTree(ctx, oid)
	if err != nil || tree.kind != choreographyPreparedCarrierKind {
		return ChoreographyPreparedCarrier{}, fmt.Errorf(invalidChoreographyPreparedCarrierError)
	}
	document, err := m.Read(ctx, ChainTip{OID: oid, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, maxDocumentBytes)
	if err != nil {
		return ChoreographyPreparedCarrier{}, err
	}
	var carrier ChoreographyPreparedCarrier
	if strictJSON(document, &carrier) != nil {
		return ChoreographyPreparedCarrier{}, fmt.Errorf(invalidChoreographyPreparedCarrierError)
	}
	expected, err := choreographyPreparedCarrierParents(carrier)
	if err != nil {
		return ChoreographyPreparedCarrier{}, err
	}
	parents, err := m.plumbing.CommitParents(ctx, oid)
	if err != nil || !slices.Equal(parents, expected) {
		return ChoreographyPreparedCarrier{}, fmt.Errorf(invalidChoreographyPreparedCarrierError)
	}
	return carrier, nil
}
