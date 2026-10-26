// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

// ChoreographyInputFinality binds one immediate predecessor's exact settled
// identity. Pure virtual gates have no Git object and use their deterministic
// derived status; native and command vertices name their signed final object.
type ChoreographyInputFinality struct {
	Task      string `json:"task"`
	OID       string `json:"oid,omitempty"`
	Succeeded bool   `json:"succeeded"`
}

// ChoreographyReadyInputsDigest is shared by the first-node broker and each
// peer interpreter. It visits only the selected task's direct predecessor
// edges, once when that task becomes ready, and includes the pinned run identity.
func ChoreographyReadyInputsDigest(binding ChoreographyRunBinding, task string,
	predecessors []ChoreographyInputFinality) (string, error) {
	if !binding.valid() || task == "" {
		return "", fmt.Errorf("execution: invalid ready-input identity")
	}
	ordered := append([]ChoreographyInputFinality(nil), predecessors...)
	slices.SortFunc(ordered, func(a, b ChoreographyInputFinality) int {
		if a.Task < b.Task {
			return -1
		}
		if a.Task > b.Task {
			return 1
		}
		return 0
	})
	for index, predecessor := range ordered {
		if predecessor.Task == "" {
			return "", fmt.Errorf("execution: empty ready predecessor")
		}
		if index > 0 && ordered[index-1].Task == predecessor.Task &&
			ordered[index-1] != predecessor {
			return "", fmt.Errorf("execution: repeated ready predecessor has conflicting finality")
		}
	}
	data, err := json.Marshal(struct {
		Binding      ChoreographyRunBinding      `json:"binding"`
		Task         string                      `json:"task"`
		Predecessors []ChoreographyInputFinality `json:"predecessors"`
	}{binding, task, ordered})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
