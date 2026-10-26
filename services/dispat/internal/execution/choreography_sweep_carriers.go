// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// A sweep export is indexed by the exact value the first node admitted into
// the plan. The signed form remains separate: it describes a path within a
// declared output root, not an absolute path in the first node's checkout.
type sweepExportKey struct{ Name, Value, Source string }

type sweepCarriedExport struct {
	signed ExportedValue
	set    *sweepSet
}

func (r *sweepOutputs) carriedExport(output plan.Output) (sweepCarriedExport, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.carried[sweepExportKey{output.Name, output.Value, output.Source}]
	return entry, ok
}

// sweepAssignmentInputs replaces only previously admitted typed path exports
// with their original signed form. It carries each producing Result and its
// verified manifest as a direct Assignment parent, so a peer can install the
// bytes before resolving the path inside its private checkout. Literal values
// retain their spelling and need no carrier.
func (c *Coordinator) sweepAssignmentInputs(outputs []plan.Output,
	sources []Source) ([]ExportedValue, []AssignmentInput, error) {
	exports := formatExports(outputs)
	if c.sweepOutputs == nil {
		return exports, nil, fmt.Errorf("execution: sweep output admission is unavailable")
	}
	inputs := make([]AssignmentInput, 0)
	seen := make(map[string]bool)
	for i, output := range outputs {
		entry, ok := c.sweepOutputs.carriedExport(output)
		if !ok {
			continue
		}
		set := entry.set
		ref := entry.signed.DeclaredPath
		if set == nil || set.manifest == nil || set.resultOID == "" || ref == nil ||
			ref.Repository != set.repositoryName || ref.Package != set.packageName ||
			entry.signed.Value != "" || set.manifest.Digest == "" {
			return nil, nil, fmt.Errorf("execution: sweep path export %s lacks its admitted result", output.Name)
		}
		exports[i] = entry.signed
		if seen[set.resultOID] {
			continue
		}
		path := ""
		found := false
		for _, source := range sources {
			if source.Name == set.repositoryName {
				path, found = source.Path, true
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("execution: sweep path export %s names a repository outside its input closure", output.Name)
		}
		inputs = append(inputs, AssignmentInput{Task: set.task, Package: set.packageName,
			Commit: set.resultOID, Digest: set.manifest.Digest, Path: path})
		seen[set.resultOID] = true
	}
	return exports, inputs, nil
}
