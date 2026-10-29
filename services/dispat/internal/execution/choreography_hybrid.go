// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	HybridCompiledSegmentName       = "compiled"
	HybridInterpretedSegmentName    = "interpreted"
	hybridMissingPolicyIdentity     = ""
	hybridPolicySchemaName          = "dispat-hybrid-policy/1"
	hybridPinnedInputsErrorText     = "execution: hybrid operation policy has incomplete pinned inputs"
	hybridTaskPolicyErrorFormat     = "execution: hybrid task %q has an invalid signed segment or pinned operation policy"
	hybridEffectTemplateErrorFormat = "execution: interpreted hybrid task %q carries executable command bytes"
)

type hybridOperationPolicy struct {
	Schema              string `json:"schema"`
	PlanDigest          string `json:"planDigest"`
	PinnedInputsDigest  string `json:"pinnedInputsDigest"`
	SemanticGraphDigest string `json:"semanticGraphDigest"`
	Task                string `json:"task"`
	Kind                string `json:"kind"`
	Stage               string `json:"stage,omitempty"`
	Segment             string `json:"segment"`
}

// BindChoreographyHybridPolicy fixes the partition and a reference to the
// exact operation in the complete immutable input closure. PlanDigest alone
// intentionally omits some native policy; the snapshot/config/invocation
// envelope binds those bytes without referring back to the graph or bootstrap.
// An empty transport frame grants no effect authority.
func BindChoreographyHybridPolicy(inventory ChoreographyLogicalGraphInventory, inputs *PinnedRunInputs) error {
	inputDigest, err := digestHybridPinnedInputs(inputs)
	if err != nil {
		return err
	}
	for index := range inventory.Tasks {
		task := &inventory.Tasks[index]
		if task.Virtual {
			continue
		}
		task.HybridSegment = hybridTaskSegment(*task)
		digest, err := digestHybridOperationPolicy(inventory, inputDigest, *task)
		if err != nil {
			return err
		}
		task.OperationPolicyDigest = digest
	}
	return nil
}

func hybridTaskSegment(task ChoreographyTaskSpec) string {
	if task.ExecutorOwned {
		return HybridInterpretedSegmentName
	}
	return HybridCompiledSegmentName
}

// Hash the shared envelope once per graph, rather than serializing every
// repository and selected config again for every task.
func digestHybridPinnedInputs(inputs *PinnedRunInputs) (string, error) {
	if inputs == nil || len(inputs.Repositories) == bootstrapNoEntriesCount ||
		len(inputs.ConfigFiles) == bootstrapNoEntriesCount || len(inputs.Selection) == bootstrapNoEntriesCount ||
		len(inputs.Options) == bootstrapNoEntriesCount {
		return hybridMissingPolicyIdentity, fmt.Errorf(hybridPinnedInputsErrorText)
	}
	document, err := json.Marshal(inputs)
	if err != nil {
		return hybridMissingPolicyIdentity, err
	}
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:]), nil
}

func digestHybridOperationPolicy(inventory ChoreographyLogicalGraphInventory, inputDigest string, task ChoreographyTaskSpec) (string, error) {
	if inputDigest == hybridMissingPolicyIdentity || inventory.Binding.PlanDigest == hybridMissingPolicyIdentity ||
		inventory.SemanticGraphDigest == hybridMissingPolicyIdentity || task.Task == hybridMissingPolicyIdentity {
		return hybridMissingPolicyIdentity, fmt.Errorf(hybridTaskPolicyErrorFormat, task.Task)
	}
	document, err := json.Marshal(hybridOperationPolicy{
		Schema: hybridPolicySchemaName, PlanDigest: inventory.Binding.PlanDigest, PinnedInputsDigest: inputDigest,
		SemanticGraphDigest: inventory.SemanticGraphDigest, Task: task.Task,
		Kind: task.Kind, Stage: task.Stage, Segment: task.HybridSegment,
	})
	if err != nil {
		return hybridMissingPolicyIdentity, err
	}
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:]), nil
}

func validateChoreographyHybridPolicy(mode string, inputs *PinnedRunInputs, carrier ChoreographyGraphCarrier) error {
	inputDigest := hybridMissingPolicyIdentity
	if mode == bootstrapHybridModeName {
		var err error
		inputDigest, err = digestHybridPinnedInputs(inputs)
		if err != nil {
			return err
		}
	}
	inventory := ChoreographyLogicalGraphInventory{Binding: carrier.ChoreographyRunBinding,
		SemanticGraphDigest: carrier.SemanticGraphDigest}
	byTask := make(map[string]ChoreographyTaskSpec, len(carrier.Tasks))
	for _, task := range carrier.Tasks {
		byTask[task.Task] = task
		if mode != bootstrapHybridModeName || task.Virtual {
			if task.HybridSegment != hybridMissingPolicyIdentity || task.OperationPolicyDigest != hybridMissingPolicyIdentity {
				return fmt.Errorf(hybridTaskPolicyErrorFormat, task.Task)
			}
			continue
		}
		digest, err := digestHybridOperationPolicy(inventory, inputDigest, task)
		if err != nil || digest != task.OperationPolicyDigest || task.HybridSegment != hybridTaskSegment(task) ||
			task.ExecutorOwned && task.Placement != PlacementOrchestrator {
			return fmt.Errorf(hybridTaskPolicyErrorFormat, task.Task)
		}
	}
	for _, template := range carrier.Templates {
		task := byTask[template.Task]
		if task.HybridSegment != HybridInterpretedSegmentName {
			continue
		}
		expected := ChoreographyCompiledTemplate{Task: task.Task, Kind: task.Kind, Stage: task.Stage,
			Weight: task.Weight, Limits: template.Limits}
		expectedDigest, err := DigestChoreographyCompiledTemplate(expected)
		actualDigest, actualErr := DigestChoreographyCompiledTemplate(template)
		if err != nil || actualErr != nil || expectedDigest != task.TemplateDigest || actualDigest != expectedDigest {
			return fmt.Errorf(hybridEffectTemplateErrorFormat, task.Task)
		}
	}
	return nil
}
