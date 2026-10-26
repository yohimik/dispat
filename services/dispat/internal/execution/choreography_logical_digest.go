// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

const incompleteLogicalGraphError = "execution: complete logical graph identity is missing"
const choreographyLogicalGraphMissingIdentity = ""
const choreographyLogicalGraphNoTasks = 0

// DigestChoreographyLogicalGraph is the pre-bootstrap, node-neutral semantic
// identity. Generated reservation OIDs and runtime eligibility never enter it.
type ChoreographyLogicalGraphInventory struct {
	Binding             ChoreographyRunBinding
	AlgorithmVersion    string
	SemanticGraphDigest string
	Tasks               []ChoreographyTaskSpec
	Templates           []ChoreographyCompiledTemplate
	PackagePolicies     []ChoreographyPackagePolicy
}

func DigestChoreographyLogicalGraph(inventory ChoreographyLogicalGraphInventory) (string, error) {
	binding, algorithmVersion := inventory.Binding, inventory.AlgorithmVersion
	semanticGraphDigest, specs := inventory.SemanticGraphDigest, inventory.Tasks
	templates, policies := inventory.Templates, inventory.PackagePolicies
	if binding.Run == choreographyLogicalGraphMissingIdentity ||
		binding.Generation == choreographyLogicalGraphMissingIdentity ||
		binding.PlanDigest == choreographyLogicalGraphMissingIdentity ||
		algorithmVersion == choreographyLogicalGraphMissingIdentity ||
		semanticGraphDigest == choreographyLogicalGraphMissingIdentity ||
		len(specs) == choreographyLogicalGraphNoTasks {
		return choreographyLogicalGraphMissingIdentity, fmt.Errorf(incompleteLogicalGraphError)
	}
	logicalTasks := slices.Clone(specs)
	for index := range logicalTasks {
		logicalTasks[index].ReservationOID = choreographyLogicalGraphMissingIdentity
		logicalTasks[index].EligibleNodes = nil
		if !logicalTasks[index].Virtual {
			logicalTasks[index].Branch = FormatChoreographyTaskBranch(binding.Run, logicalTasks[index].Task)
		}
	}
	slices.SortFunc(logicalTasks, func(a, b ChoreographyTaskSpec) int { return cmp.Compare(a.Task, b.Task) })
	logicalTemplates := slices.Clone(templates)
	slices.SortFunc(logicalTemplates, func(a, b ChoreographyCompiledTemplate) int { return cmp.Compare(a.Task, b.Task) })
	logicalPolicies := slices.Clone(policies)
	slices.SortFunc(logicalPolicies, func(a, b ChoreographyPackagePolicy) int { return cmp.Compare(a.Package, b.Package) })
	document, err := json.Marshal(struct {
		Run                 string                         `json:"run"`
		Generation          string                         `json:"generation"`
		PlanDigest          string                         `json:"planDigest"`
		AlgorithmVersion    string                         `json:"algorithmVersion"`
		SemanticGraphDigest string                         `json:"semanticGraphDigest"`
		Tasks               []ChoreographyTaskSpec         `json:"tasks"`
		Templates           []ChoreographyCompiledTemplate `json:"templates"`
		PackagePolicies     []ChoreographyPackagePolicy    `json:"packagePolicies"`
	}{binding.Run, binding.Generation, binding.PlanDigest, algorithmVersion,
		semanticGraphDigest,
		logicalTasks, logicalTemplates, logicalPolicies})
	if err != nil {
		return choreographyLogicalGraphMissingIdentity, err
	}
	sum := sha256.Sum256(document)
	return hex.EncodeToString(sum[:]), nil
}
