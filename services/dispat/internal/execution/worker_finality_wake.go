// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

const (
	workerFinalityWakeCapacity = 1
	workerFinalityNoIdentity   = ""
)

func (task *claimedTask) finalityNotifications() <-chan struct{} {
	task.finalityWakeOnce.Do(func() {
		task.finalityWake = make(chan struct{}, workerFinalityWakeCapacity)
	})
	return task.finalityWake
}

// The feed is only a wake hint. The task rereads its authenticated finality
// and exact receipt before it can release the physical lease.
func (w *Worker) notifyClaimedFinality(binding ChoreographyRunBinding, events []ChoreographyEvent) {
	w.claims.Lock()
	defer w.claims.Unlock()
	for _, event := range events {
		if event.Kind != ChoreographyTerminal && event.Kind != ChoreographyRetryReady &&
			event.Kind != ChoreographyPickupSettled {
			continue
		}
		if event.Node != w.Node || event.ClaimOID == workerFinalityNoIdentity ||
			event.PickupID == workerFinalityNoIdentity || event.GraphRootOID == workerFinalityNoIdentity {
			continue
		}
		for identity, task := range w.claimed {
			intent := task.slotLease.Intent
			recovery := intent.Recovery
			if recovery == nil || recovery.Binding != binding || recovery.Node != w.Node ||
				recovery.GraphRootOID != event.GraphRootOID || identity.ClaimOID != event.ClaimOID ||
				intent.Run != binding.Run || intent.Task != event.Task || intent.Attempt != event.Attempt ||
				intent.PickupID != event.PickupID {
				continue
			}
			task.finalityNotifications()
			select {
			case task.finalityWake <- struct{}{}:
			default:
			}
		}
	}
}
