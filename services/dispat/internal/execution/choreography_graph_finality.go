// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

const (
	compiledGraphIsIncomplete = false
	compiledGraphIsTerminal   = true
)

// IsCompiledGraphTerminal is the normal-completion fence for the first-node
// picker. Once every executable task has a signed logical terminal, no new
// claim can call WaitGroup.Add while physical leases await finality.
func (b *FirstNodeBroker) IsCompiledGraphTerminal() bool {
	if b == nil {
		return compiledGraphIsIncomplete
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, task := range b.tasks {
		if !task.spec.Virtual && (!task.terminal || task.pendingPackageSettlement ||
			task.pendingNoClaim || task.pendingInertCancel) {
			return compiledGraphIsIncomplete
		}
	}
	return compiledGraphIsTerminal
}
