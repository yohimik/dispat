// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

func (w *Worker) pollNotifications() <-chan struct{} {
	w.pollWakeOnce.Do(func() { w.pollWake = make(chan struct{}, 1) })
	return w.pollWake
}

func (w *Worker) notifyPoll() {
	w.pollNotifications()
	select {
	case w.pollWake <- struct{}{}:
	default:
	}
}
