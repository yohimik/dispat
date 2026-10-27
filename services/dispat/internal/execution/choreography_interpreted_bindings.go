// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	interpretedBindingMissingIdentity            = ""
	interpretedBindingMismatchErrorFormat        = "%s: %s"
	interpretedBindingRunMismatchReason          = "run, graph, task or template identity differs"
	interpretedBindingPredecessorMismatchReason  = "signed predecessor identities differ"
	interpretedBindingSourceCountMismatchReason  = "source repository count differs from the private plan"
	interpretedBindingSourceLayoutMismatchReason = "source repository name or path differs from the private plan"
	interpretedBindingPlannedHeadMismatchReason  = "source planned head differs from the private plan"
	interpretedBindingSnapshotMismatchReason     = "source snapshot is absent or has a different planned parent"
	interpretedBindingComputedMismatchReason     = "effective environment, exports or inputs differ from the private frame"
)

func interpretedBindingMismatch(reason string) error {
	return fmt.Errorf(interpretedBindingMismatchErrorFormat, invalidChoreographyPreparedCarrierError, reason)
}

// ChoreographyInterpretedBindingOptions supplies independently planned inputs
// and signed admitted predecessor state. The prepared carrier supplies source
// snapshot OIDs only after their repository layout and planned heads agree.
type ChoreographyInterpretedBindingOptions struct {
	Request        release.StageRequest
	PreparedOID    string
	State          ChoreographyVerifiedBindingState
	Sources        []Source
	InputProviders []InputPackage
	Mailbox        *GitMailbox
	Store          *gitx.LocalGitx
	Binding        ChoreographyRunBinding
	GraphRootOID   string
	TemplateDigest string
	MaxBytes       int64
	Sweep          bool
}

// ResolveInterpretedChoreographyBindings reconstructs the effective bindings
// from the private plan and authenticated predecessor results. It creates no
// remote ref, ledger event, or source snapshot. Importing a verified Result
// into the local assignment store is the same bounded object-only operation
// as the compiled binding resolver.
func ResolveInterpretedChoreographyBindings(ctx context.Context,
	options ChoreographyInterpretedBindingOptions) (ChoreographyTaskBindings, error) {
	request, state := options.Request, options.State
	if options.Mailbox == nil || options.Store == nil || !options.Binding.valid() ||
		options.GraphRootOID == interpretedBindingMissingIdentity ||
		options.TemplateDigest == interpretedBindingMissingIdentity ||
		options.PreparedOID == interpretedBindingMissingIdentity ||
		state.PreparedInputOID != options.PreparedOID ||
		request.TaskID == interpretedBindingMissingIdentity ||
		request.Release == nil || request.Release.Pkg == nil ||
		options.MaxBytes <= choreographyPreparedCarrierNoDocumentBytes {
		return ChoreographyTaskBindings{}, fmt.Errorf(missingChoreographyPreparedCarrierError)
	}
	carrier, err := options.Mailbox.ReadChoreographyPreparedCarrier(ctx, options.PreparedOID, options.MaxBytes)
	if err != nil {
		return ChoreographyTaskBindings{}, err
	}
	if carrier.ChoreographyRunBinding != options.Binding ||
		carrier.GraphRootOID != options.GraphRootOID ||
		carrier.Task != request.TaskID || carrier.TemplateDigest != options.TemplateDigest {
		return ChoreographyTaskBindings{}, interpretedBindingMismatch(interpretedBindingRunMismatchReason)
	}
	if !slices.Equal(carrier.PredecessorResultOIDs, state.PredecessorTaskOIDs) {
		return ChoreographyTaskBindings{}, interpretedBindingMismatch(interpretedBindingPredecessorMismatchReason)
	}
	if len(carrier.Bindings.Repositories) != len(options.Sources) {
		return ChoreographyTaskBindings{}, interpretedBindingMismatch(interpretedBindingSourceCountMismatchReason)
	}
	for index, source := range options.Sources {
		signed := carrier.Bindings.Repositories[index]
		if signed.Name != source.Name || signed.Path != source.Path {
			return ChoreographyTaskBindings{}, interpretedBindingMismatch(interpretedBindingSourceLayoutMismatchReason)
		}
		if signed.PlannedHead != source.Head {
			return ChoreographyTaskBindings{}, interpretedBindingMismatch(interpretedBindingPlannedHeadMismatchReason)
		}
		if signed.Snapshot == interpretedBindingMissingIdentity {
			return ChoreographyTaskBindings{}, interpretedBindingMismatch(interpretedBindingSnapshotMismatchReason)
		}
		parents, parentErr := options.Mailbox.plumbing.CommitParents(ctx, signed.Snapshot)
		if parentErr != nil || len(parents) != bootstrapSnapshotParentCount ||
			parents[bootstrapFirstEntryIndex] != source.Head {
			return ChoreographyTaskBindings{}, interpretedBindingMismatch(interpretedBindingSnapshotMismatchReason)
		}
	}
	dir, err := resolvePackageDir(options.Sources, request)
	if err != nil {
		return ChoreographyTaskBindings{}, err
	}
	bindings := ChoreographyTaskBindings{
		Repositories:          slices.Clone(carrier.Bindings.Repositories),
		Exports:               formatExports(request.Release.Outputs),
		Env:                   slices.Clone(request.Env),
		PredecessorResultOIDs: slices.Clone(state.PredecessorTaskOIDs),
	}
	if options.Sweep || request.Descriptor.Inputs != release.InputsNone {
		imported := make(map[string]bool)
		seen := make(map[choreographyInputDestination]bool)
		if err := applyVerifiedChoreographyExports(ctx, &bindings, choreographyCarriedExportInputs{
			Outputs: request.Release.Outputs, Verified: state.VerifiedCarriedExports,
			Sources: options.Sources, SourceDir: options.Mailbox.plumbing.Dir,
			Imported: imported, Seen: seen, ImportCommit: options.Store.ImportCommitFrom,
		}); err != nil {
			return ChoreographyTaskBindings{}, err
		}
		if !options.Sweep {
			appendOutput := func(output ChoreographyVerifiedOutput, path string) error {
				if output.ResultOID == interpretedBindingMissingIdentity || output.Manifest == nil ||
					output.Manifest.Digest == interpretedBindingMissingIdentity ||
					output.Task == interpretedBindingMissingIdentity || output.Package == interpretedBindingMissingIdentity {
					return fmt.Errorf(choreographyBindingMissingOutputIdentityError)
				}
				return appendVerifiedChoreographyInput(ctx, choreographyVerifiedInputAppend{
					Bindings: &bindings, Input: AssignmentInput{Task: output.Task,
						Package: output.Package, Commit: output.ResultOID,
						Digest: output.Manifest.Digest, Path: path},
					SourceDir: options.Mailbox.plumbing.Dir, Imported: imported, Seen: seen,
					ImportCommit: options.Store.ImportCommitFrom,
				})
			}
			for _, provider := range options.InputProviders {
				output, found := state.VerifiedOutputByPackage[provider.Package]
				if !found {
					if provider.IsPrepared {
						return ChoreographyTaskBindings{}, fmt.Errorf(
							choreographyBindingMissingPreparedProviderOutputErrorFormat,
							provider.Package, request.TaskID)
					}
					continue
				}
				if err := appendOutput(output, provider.Path); err != nil {
					return ChoreographyTaskBindings{}, err
				}
			}
			if request.Descriptor.Inputs == release.InputsOwnOutputs {
				stages := request.Descriptor.OwnOutputStages
				if len(stages) == choreographyBindingNoEntries {
					stages = []string{choreographyOwnBuildStage}
				}
				for _, stage := range stages {
					output, found := state.VerifiedOutputByStage[stage]
					if !found {
						continue
					}
					if err := appendOutput(output, resolveAnchoredPath(options.Sources,
						request.Release.Pkg.Repository, dir)); err != nil {
						return ChoreographyTaskBindings{}, err
					}
				}
			}
		}
	}
	expectedDigest, err := DigestChoreographyTaskBindings(bindings)
	if err != nil {
		return ChoreographyTaskBindings{}, err
	}
	signedDigest, err := DigestChoreographyTaskBindings(carrier.Bindings)
	if err != nil || expectedDigest != signedDigest {
		return ChoreographyTaskBindings{}, interpretedBindingMismatch(interpretedBindingComputedMismatchReason)
	}
	bindings.PreparedInputOID = options.PreparedOID
	return bindings, nil
}
