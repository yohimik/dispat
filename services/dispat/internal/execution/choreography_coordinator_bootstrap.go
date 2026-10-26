// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const (
	choreographyCoordinatorBootstrapMissingIdentity = ""
	choreographyCoordinatorBootstrapNoManifestBytes = 0
	choreographyCoordinatorBootstrapNoPackBytes     = 0
	choreographyCoordinatorBootstrapIncompleteError = "execution: incomplete choreography bootstrap preparation"
	choreographyCoordinatorBootstrapEndpointError   = "execution: choreography requires one canonical mailbox endpoint"
)

type CoordinatorBootstrapSources struct {
	TagSources     []ChoreographyTagSource
	SinceSourceDir string
}

// PrepareChoreographyBootstrap fixes the private planning inputs and return
// identity before preflight. It writes only local objects; task and ledger
// reservations later make this commit reachable in the canonical mailbox.
func (c *Coordinator) PrepareChoreographyBootstrap(ctx context.Context,
	sources CoordinatorBootstrapSources) error {
	if c == nil || c.BootstrapOID != choreographyCoordinatorBootstrapMissingIdentity || c.canonicalMailbox == nil ||
		c.CanonicalEndpoint == choreographyCoordinatorBootstrapMissingIdentity || c.AlgorithmVersion == choreographyCoordinatorBootstrapMissingIdentity ||
		c.PinnedInputs == nil || c.PinnedSources == nil || c.GraphDigest == choreographyCoordinatorBootstrapMissingIdentity ||
		c.ReturnEndpoint == choreographyCoordinatorBootstrapMissingIdentity || c.ReturnCertSHA256 == choreographyCoordinatorBootstrapMissingIdentity ||
		c.Limits.MaxManifestBytes <= choreographyCoordinatorBootstrapNoManifestBytes ||
		c.Limits.MaxPackBytes <= choreographyCoordinatorBootstrapNoPackBytes {
		return fmt.Errorf(choreographyCoordinatorBootstrapIncompleteError)
	}
	mailbox := c.canonicalMailbox
	for _, link := range c.Links {
		if link.Endpoint != c.CanonicalEndpoint || c.mailboxes[link.Name] == nil {
			return fmt.Errorf(choreographyCoordinatorBootstrapEndpointError)
		}
	}
	oid, err := mailbox.PrepareChoreographyBootstrap(ctx, ChoreographyBootstrapOptions{
		Bootstrap: &RunBootstrap{Protocol: ChoreographyProtocolVersion,
			Mode: c.Mode, AlgorithmVersion: c.AlgorithmVersion,
			JoinPolicy: c.JoinPolicy,
			ChoreographyRunBinding: ChoreographyRunBinding{
				Run: c.Run, Generation: c.Generation,
				PlanDigest: c.PlanDigest, GraphDigest: c.GraphDigest,
			},
			CoordinationEndpoint: c.CanonicalEndpoint,
			ReturnEndpoint:       c.ReturnEndpoint, ReturnCertSHA256: c.ReturnCertSHA256,
			PinnedInputs: *c.PinnedInputs},
		MaxDocumentBytes: c.Limits.MaxManifestBytes,
		MaxTagBytes:      c.Limits.MaxPackBytes,
		TagSources:       sources.TagSources,
		SinceSourceDir:   sources.SinceSourceDir,
	})
	if err != nil {
		return err
	}
	c.BootstrapOID = oid
	return nil
}
