// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

type probeWaiterKey struct {
	node   string
	branch string
}

func (c *Coordinator) watchProbeReply(node, branch string) (<-chan struct{}, func()) {
	key := probeWaiterKey{node: node, branch: branch}
	wake := make(chan struct{}, 1)
	c.probeWaiters.Store(key, wake)
	return wake, func() { c.probeWaiters.Delete(key) }
}

func (c *Coordinator) notifyProbeReply(node, branch string) {
	waiter, found := c.probeWaiters.Load(probeWaiterKey{node: node, branch: branch})
	if !found {
		return
	}
	select {
	case waiter.(chan struct{}) <- struct{}{}:
	default:
	}
}
