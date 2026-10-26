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

const (
	invalidCompiledTemplateErrorFormat    = "execution: compiled template for task %q is invalid: %s"
	invalidTaskBindingsErrorFormat        = "execution: input bindings for task %q are invalid: %s"
	invalidCompiledTemplateIdentityReason = "task, kind or positive weight is missing"
	invalidCompiledTemplateClaimReason    = "claim header or graph identity differs"
	invalidCompiledTemplateStageReason    = "stage kind and name differ"
	invalidTaskBindingsSourceReason       = "executable frame has no source repository"
	invalidCompiledTemplateContentReason  = "accepted assignment changed immutable executable content"
	invalidCompiledTemplateDigestReason   = "accepted assignment names another signed template digest"
	compiledTemplateMissingIdentity       = ""
	compiledTemplateNoDigest              = ""
	compiledTemplateNonpositiveWeight     = 0
	compiledTemplateNoSourceRepositories  = 0
	taskBindingsEncodeErrorFormat         = "execution: task input bindings cannot be encoded: %w"
	minimumCompiledAttempt                = 1
)

// ChoreographyCompiledTemplate is the immutable executable part of one
// logical task. Its missing input fields are supplied only from verified
// predecessor, preparation, and source events when the task becomes ready.
type ChoreographyCompiledTemplate struct {
	Task            string             `json:"task"`
	Kind            string             `json:"kind"`
	Stage           string             `json:"stage,omitempty"`
	Weight          int                `json:"weight"`
	Package         *AssignmentPackage `json:"package,omitempty"`
	Frame           *AssignmentFrame   `json:"frame,omitempty"`
	Script          string             `json:"script,omitempty"`
	StaticEnv       []string           `json:"staticEnv,omitempty"`
	Shell           []string           `json:"shell,omitempty"`
	Platforms       []string           `json:"platforms,omitempty"`
	Outputs         []string           `json:"outputs,omitempty"`
	Permits         AssignmentPermits  `json:"permits"`
	DeadlineSeconds int                `json:"deadlineSeconds,omitempty"`
	Limits          TransferLimits     `json:"limits"`
}

// VerifyChoreographyAssignmentTemplate checks the executable bytes carried
// by an accepted signed Assignment against the exact graph-root template.
func VerifyChoreographyAssignmentTemplate(template ChoreographyCompiledTemplate,
	assignment Assignment) error {
	actual := ChoreographyCompiledTemplate{Task: assignment.Task, Kind: assignment.Kind,
		Stage: assignment.Stage, Weight: assignment.Weight,
		Package: assignment.Package, Frame: assignment.Frame,
		Script: assignment.Script, StaticEnv: assignment.StaticEnv,
		Shell: assignment.Shell, Platforms: assignment.Platforms,
		Outputs: assignment.Outputs, Permits: assignment.Permits,
		DeadlineSeconds: assignment.DeadlineSeconds, Limits: assignment.Limits}
	digest, err := DigestChoreographyCompiledTemplate(template)
	if err != nil {
		return err
	}
	if assignment.TemplateDigest != digest {
		return fmt.Errorf(invalidCompiledTemplateErrorFormat, template.Task,
			invalidCompiledTemplateDigestReason)
	}
	actualDigest, err := DigestChoreographyCompiledTemplate(actual)
	if err != nil {
		return err
	}
	if actualDigest != digest {
		return fmt.Errorf(invalidCompiledTemplateErrorFormat, template.Task,
			invalidCompiledTemplateContentReason)
	}
	return nil
}

// ChoreographyTaskBindings names the effective inputs of one attempt. The
// explicit result and prepared-state IDs make the source of every filled
// field checkable against the signed frontier before a claim can be accepted.
type ChoreographyTaskBindings struct {
	Repositories          []AssignmentRepository `json:"repositories,omitempty"`
	Inputs                []AssignmentInput      `json:"inputs,omitempty"`
	Exports               []ExportedValue        `json:"exports,omitempty"`
	Env                   []string               `json:"env,omitempty"`
	PredecessorResultOIDs []string               `json:"predecessorResultOids,omitempty"`
	PreparedInputOID      string                 `json:"preparedInputOid,omitempty"`
}

func DigestChoreographyCompiledTemplate(template ChoreographyCompiledTemplate) (string, error) {
	if template.Task == compiledTemplateMissingIdentity || template.Kind == compiledTemplateMissingIdentity ||
		template.Weight <= compiledTemplateNonpositiveWeight {
		return compiledTemplateNoDigest, fmt.Errorf(invalidCompiledTemplateErrorFormat, template.Task, invalidCompiledTemplateIdentityReason)
	}
	document, err := json.Marshal(template)
	if err != nil {
		return compiledTemplateNoDigest, fmt.Errorf(invalidCompiledTemplateErrorFormat, template.Task, err)
	}
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:]), nil
}

func DigestChoreographyTaskBindings(bindings ChoreographyTaskBindings) (string, error) {
	document, err := json.Marshal(bindings)
	if err != nil {
		return compiledTemplateNoDigest, fmt.Errorf(taskBindingsEncodeErrorFormat, err)
	}
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:]), nil
}

type ChoreographyTemplateBindingOptions struct {
	Template     ChoreographyCompiledTemplate
	Bindings     ChoreographyTaskBindings
	Header       Header
	BootstrapOID string
	GraphDigest  string
}

// BindChoreographyCompiledTemplate fills only the template's declared input
// fields. The caller must verify the bindings against signed finality before
// accepting a claim and must sign the resulting Assignment on the task ref.
func BindChoreographyCompiledTemplate(options ChoreographyTemplateBindingOptions) (Assignment, error) {
	template, bindings, header, bootstrapOID, graphDigest := options.Template, options.Bindings, options.Header, options.BootstrapOID, options.GraphDigest

	templateDigest, err := DigestChoreographyCompiledTemplate(template)
	if err != nil {
		return Assignment{}, err
	}
	bindingsDigest, err := DigestChoreographyTaskBindings(bindings)
	if err != nil {
		return Assignment{}, err
	}
	if header.Task != template.Task || header.Kind != template.Kind || header.Attempt < minimumCompiledAttempt ||
		header.Node == compiledTemplateMissingIdentity || header.Branch == compiledTemplateMissingIdentity ||
		bootstrapOID == compiledTemplateMissingIdentity || graphDigest == compiledTemplateMissingIdentity {
		return Assignment{}, fmt.Errorf(invalidCompiledTemplateErrorFormat, template.Task, invalidCompiledTemplateClaimReason)
	}
	if template.Kind == KindStage && template.Stage == compiledTemplateMissingIdentity ||
		template.Kind != KindStage && template.Stage != compiledTemplateMissingIdentity {
		return Assignment{}, fmt.Errorf(invalidCompiledTemplateErrorFormat, template.Task, invalidCompiledTemplateStageReason)
	}
	if template.Frame != nil && len(bindings.Repositories) == compiledTemplateNoSourceRepositories {
		return Assignment{}, fmt.Errorf(invalidTaskBindingsErrorFormat, template.Task, invalidTaskBindingsSourceReason)
	}
	assignment := Assignment{Header: header, Weight: template.Weight,
		GraphDigest: graphDigest, BootstrapOID: bootstrapOID,
		TemplateDigest: templateDigest, BindingsDigest: bindingsDigest,
		Repositories: slices.Clone(bindings.Repositories),
		Script:       template.Script, Stage: template.Stage,
		Env: slices.Clone(bindings.Env), StaticEnv: slices.Clone(template.StaticEnv),
		Exports: slices.Clone(bindings.Exports), Shell: slices.Clone(template.Shell),
		Platforms: slices.Clone(template.Platforms), Inputs: slices.Clone(bindings.Inputs),
		Outputs: slices.Clone(template.Outputs), Permits: template.Permits,
		DeadlineSeconds: template.DeadlineSeconds, Limits: template.Limits}
	if template.Package != nil {
		packageSpec := *template.Package
		packageSpec.OutputRoots = slices.Clone(template.Package.OutputRoots)
		assignment.Package = &packageSpec
	}
	if template.Frame != nil {
		frame := *template.Frame
		frame.Before = slices.Clone(template.Frame.Before)
		frame.Commands = slices.Clone(template.Frame.Commands)
		frame.After = slices.Clone(template.Frame.After)
		assignment.Frame = &frame
	}
	return assignment, nil
}
