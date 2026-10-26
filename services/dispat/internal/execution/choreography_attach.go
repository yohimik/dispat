// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

// ChoreographyMailbox is the single authoritative coordination object store.
// The first node constructs the broker only after the initial refs have been
// read back at their exact prepared OIDs.
func (c *Coordinator) ChoreographyMailbox() (*GitMailbox, error) {
	if c == nil || c.canonicalMailbox == nil || !c.choreographyInitialized || c.GraphRootOID == "" {
		return nil, fmt.Errorf("execution: choreography refs are not initialized")
	}
	mailbox := c.canonicalMailbox
	if mailbox == nil {
		return nil, fmt.Errorf("execution: canonical choreography mailbox is unavailable")
	}
	return mailbox, nil
}

// ChoreographyRefAdvanced updates the exact cleanup lease after the first
// node has read back an atomic task/slot/ledger transaction.
func (c *Coordinator) ChoreographyRefAdvanced(ctx context.Context, ref ChoreographyRefAdvance) {
	if c == nil || c.canonicalMailbox == nil || ref.Branch == "" || ref.NewOID == "" {
		return
	}
	c.recordOwnedRef(ctx, ownedRefStep{node: c.Local.Name,
		branch: ref.Branch, parent: ref.OldOID, oid: ref.NewOID})
}

// AttachChoreography installs the exact broker and persistence pair before
// frames start. It never grants a second endpoint Git write authority.
func (c *Coordinator) AttachChoreography(persist *GitChoreographyPersist, broker *FirstNodeBroker) error {
	if _, err := c.ChoreographyMailbox(); err != nil {
		return err
	}
	if persist == nil || broker == nil || c.choreographyPersist != nil || c.choreographyBroker != nil ||
		persist.opts.Binding.Run != c.Run || broker.binding != persist.opts.Binding ||
		persist.opts.Mailbox != c.canonicalMailbox {
		return fmt.Errorf("execution: choreography broker does not match initialized run")
	}
	c.choreographyPersist, c.choreographyBroker = persist, broker
	// V3 workers return through the broker. The older task-ref watcher has no
	// active attempts at this pre-frame boundary and must not poll the same
	// canonical refs as an independent v2 protocol.
	if c.stopWatching != nil {
		c.stopWatching()
		c.watching.Wait()
		c.stopWatching = nil
	}
	return nil
}
