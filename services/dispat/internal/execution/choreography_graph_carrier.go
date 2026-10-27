// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

const choreographyGraphRootKind MessageKind = "choreography-graph-root"
const choreographyGraphRootPrefix = "dispat-graph-"
const choreographyGraphRefNamespace = "refs/heads/"
const choreographyGraphTaskRefPatternSuffix = "-*"
const graphRootCoherentReadAttempts = 4
const graphCarrierMissingIdentity = ""
const graphCarrierNoTasks = 0
const graphCarrierNoManifestBytes = 0
const graphCarrierFirstReadAttempt = 0
const graphCarrierTemplateSeen = true
const graphCarrierReadCoherent = true
const graphCarrierReadIncoherent = false
const graphCarrierFirstParentIndex = 0
const graphCarrierSingleParentCount = 1
const graphCarrierInitHistoryMinimum = 2
const graphCarrierInitEventIndex = 1
const graphCarrierInitParentCount = 2
const graphCarrierInitSequence = 1

const (
	graphCarrierIncompleteErrorMessage       = "execution: compiled graph carrier has incomplete run identity or tasks"
	graphCarrierRepeatedTaskErrorFormat      = "execution: compiled graph carrier repeats task %q"
	graphCarrierTaskRootErrorFormat          = "execution: compiled task %q lacks its exact root or template"
	graphCarrierVirtualTaskErrorFormat       = "execution: virtual task %q has executable state"
	graphCarrierTemplateLookupErrorFormat    = "execution: compiled template %q is absent, virtual or duplicate"
	graphCarrierTemplateDigestErrorFormat    = "execution: compiled template %q digest cannot be computed: %w"
	graphCarrierTemplateMismatchErrorFormat  = "execution: compiled template %q differs from its task descriptor"
	graphCarrierTemplateMissingErrorFormat   = "execution: executable task %q has no compiled template"
	graphCarrierEnumerationDigestErrorFormat = "execution: complete compiled graph enumeration cannot be digested: %w"
	graphCarrierManifestMismatchErrorMessage = "execution: compiled graph carrier digest differs from complete enumeration"
	graphCarrierLogicalMismatchErrorMessage  = "execution: compiled graph digest does not cover the complete logical descriptor inventory"
	graphRootPublishStateErrorMessage        = "execution: canonical graph root cannot be published from this run"
	graphRootManifestBytesErrorMessage       = "execution: complete compiled graph carrier exceeds the signed manifest byte limit"
	graphRootReadIdentityErrorMessage        = "execution: compiled graph read has incomplete identity"
	graphRootAbsentErrorMessage              = "execution: compiled graph root is absent"
	graphRootBootstrapParentErrorMessage     = "execution: compiled graph root has another bootstrap parent"
	graphRootSignedContentErrorMessage       = "execution: compiled graph root has invalid signed content"
	graphRootRunMismatchErrorMessage         = "execution: compiled graph root differs from pinned run"
	graphRootTaskRefCountErrorMessage        = "execution: canonical task ref count differs from compiled graph"
	graphRootAdditionalTaskRefErrorFormat    = "execution: canonical task ref %q is additional to compiled graph"
	graphRootTaskRootMismatchErrorFormat     = "execution: canonical task ref %q differs from compiled root"
	graphRootInitLedgerAbsentErrorMessage    = "execution: compiled graph has no initialization-complete ledger"
	graphRootInitEventAbsentErrorMessage     = "execution: compiled graph has no initialization-complete ledger event"
	graphRootLedgerParentErrorMessage        = "execution: graph ledger root has another bootstrap parent"
	graphRootInitParentErrorMessage          = "execution: initialization-complete event lacks exact graph parent"
	graphRootInitKindErrorMessage            = "execution: initialization-complete event has another signed kind"
	graphRootInitIdentityErrorMessage        = "execution: initialization-complete event differs from pinned graph root"
)

// ChoreographyGraphCarrier is the signed complete compiled enumeration.
// It is made reachable only after every canonical task ref has settled.
type ChoreographyGraphCarrier struct {
	ChoreographyRunBinding
	BootstrapOID            string                         `json:"bootstrapOid"`
	AlgorithmVersion        string                         `json:"algorithmVersion"`
	SemanticGraphDigest     string                         `json:"semanticGraphDigest"`
	LogicalDescriptorDigest string                         `json:"logicalDescriptorDigest"`
	ManifestDigest          string                         `json:"manifestDigest"`
	Tasks                   []ChoreographyTaskSpec         `json:"tasks"`
	Templates               []ChoreographyCompiledTemplate `json:"templates"`
	PackagePolicies         []ChoreographyPackagePolicy    `json:"packagePolicies,omitempty"`
}

func FormatChoreographyGraphRootBranch(run string) string {
	return choreographyGraphRootPrefix + run
}

func validateChoreographyGraphCarrier(carrier ChoreographyGraphCarrier) error {
	if !carrier.ChoreographyRunBinding.valid() || carrier.BootstrapOID == graphCarrierMissingIdentity ||
		carrier.AlgorithmVersion == graphCarrierMissingIdentity || carrier.SemanticGraphDigest == graphCarrierMissingIdentity ||
		len(carrier.Tasks) == graphCarrierNoTasks {
		return fmt.Errorf(graphCarrierIncompleteErrorMessage)
	}
	byTask := make(map[string]ChoreographyTaskSpec, len(carrier.Tasks))
	for _, task := range carrier.Tasks {
		if task.Task == graphCarrierMissingIdentity || byTask[task.Task].Task != graphCarrierMissingIdentity {
			return fmt.Errorf(graphCarrierRepeatedTaskErrorFormat, task.Task)
		}
		if !task.Virtual && (!validChoreographyAttemptLimit(task.EffectiveMaxAttempts()) ||
			task.Branch != FormatChoreographyTaskBranch(carrier.Run, task.Task) ||
			task.ReservationOID == graphCarrierMissingIdentity || task.TemplateDigest == graphCarrierMissingIdentity) {
			return fmt.Errorf(graphCarrierTaskRootErrorFormat, task.Task)
		}
		if task.Virtual && (task.Branch != graphCarrierMissingIdentity || task.ReservationOID != graphCarrierMissingIdentity || task.TemplateDigest != graphCarrierMissingIdentity) {
			return fmt.Errorf(graphCarrierVirtualTaskErrorFormat, task.Task)
		}
		byTask[task.Task] = task
	}
	seen := make(map[string]bool, len(carrier.Templates))
	for _, template := range carrier.Templates {
		task := byTask[template.Task]
		if task.Task == graphCarrierMissingIdentity || task.Virtual || seen[template.Task] {
			return fmt.Errorf(graphCarrierTemplateLookupErrorFormat, template.Task)
		}
		digest, err := DigestChoreographyCompiledTemplate(template)
		if err != nil {
			return fmt.Errorf(graphCarrierTemplateDigestErrorFormat, template.Task, err)
		}
		if digest != task.TemplateDigest || template.Weight != task.Weight ||
			template.Kind != task.Kind || template.Stage != task.Stage {
			return fmt.Errorf(graphCarrierTemplateMismatchErrorFormat, template.Task)
		}
		seen[template.Task] = graphCarrierTemplateSeen
	}
	for _, task := range carrier.Tasks {
		if !task.Virtual && !seen[task.Task] {
			return fmt.Errorf(graphCarrierTemplateMissingErrorFormat, task.Task)
		}
	}
	logical, manifest, err := digestChoreographyGraphCarrier(carrier)
	if err != nil {
		return fmt.Errorf(graphCarrierEnumerationDigestErrorFormat, err)
	}
	if carrier.LogicalDescriptorDigest != logical || carrier.ManifestDigest != manifest {
		return fmt.Errorf(graphCarrierManifestMismatchErrorMessage)
	}
	if logical != carrier.GraphDigest {
		return fmt.Errorf(graphCarrierLogicalMismatchErrorMessage)
	}
	return nil
}

func digestChoreographyGraphCarrier(carrier ChoreographyGraphCarrier) (string, string, error) {
	logical, err := DigestChoreographyLogicalGraph(ChoreographyLogicalGraphInventory{
		Binding: carrier.ChoreographyRunBinding, AlgorithmVersion: carrier.AlgorithmVersion,
		SemanticGraphDigest: carrier.SemanticGraphDigest, Tasks: carrier.Tasks,
		Templates: carrier.Templates, PackagePolicies: carrier.PackagePolicies})
	if err != nil {
		return graphCarrierMissingIdentity, graphCarrierMissingIdentity, err
	}
	carrier.LogicalDescriptorDigest, carrier.ManifestDigest = logical, graphCarrierMissingIdentity
	manifestDocument, err := json.Marshal(carrier)
	if err != nil {
		return graphCarrierMissingIdentity, graphCarrierMissingIdentity, err
	}
	manifestSum := sha256.Sum256(manifestDocument)
	return logical, hex.EncodeToString(manifestSum[:]), nil
}

type ChoreographyGraphRootPublication struct {
	Tasks               []ChoreographyTaskSpec
	Templates           []ChoreographyCompiledTemplate
	PackagePolicies     []ChoreographyPackagePolicy
	SemanticGraphDigest string
}

func (c *Coordinator) PublishChoreographyGraphRoot(ctx context.Context,
	publication ChoreographyGraphRootPublication) (string, error) {
	if c == nil || c.canonicalMailbox == nil || c.CanonicalEndpoint == graphCarrierMissingIdentity || c.GraphRootOID != graphCarrierMissingIdentity {
		return graphCarrierMissingIdentity, fmt.Errorf(graphRootPublishStateErrorMessage)
	}
	carrier := ChoreographyGraphCarrier{ChoreographyRunBinding: ChoreographyRunBinding{
		Run: c.Run, Generation: c.Generation, PlanDigest: c.PlanDigest, GraphDigest: c.GraphDigest},
		BootstrapOID: c.BootstrapOID, AlgorithmVersion: c.AlgorithmVersion,
		SemanticGraphDigest: publication.SemanticGraphDigest,
		Tasks:               slices.Clone(publication.Tasks), Templates: slices.Clone(publication.Templates),
		PackagePolicies: slices.Clone(publication.PackagePolicies)}
	slices.SortFunc(carrier.Tasks, func(a, b ChoreographyTaskSpec) int { return cmp.Compare(a.Task, b.Task) })
	slices.SortFunc(carrier.Templates, func(a, b ChoreographyCompiledTemplate) int { return cmp.Compare(a.Task, b.Task) })
	slices.SortFunc(carrier.PackagePolicies, func(a, b ChoreographyPackagePolicy) int { return cmp.Compare(a.Package, b.Package) })
	logical, manifest, err := digestChoreographyGraphCarrier(carrier)
	if err != nil {
		return graphCarrierMissingIdentity, err
	}
	carrier.LogicalDescriptorDigest, carrier.ManifestDigest = logical, manifest
	if err := validateChoreographyGraphCarrier(carrier); err != nil {
		return graphCarrierMissingIdentity, err
	}
	document, err := json.Marshal(carrier)
	if err != nil {
		return graphCarrierMissingIdentity, err
	}
	if int64(len(document)) > c.Limits.MaxManifestBytes {
		return graphCarrierMissingIdentity, fmt.Errorf(graphRootManifestBytesErrorMessage)
	}
	oid, err := c.canonicalMailbox.commitMessage(ctx, choreographyGraphRootKind,
		document, []string{c.BootstrapOID}, nil)
	if err != nil {
		return graphCarrierMissingIdentity, err
	}
	branch := FormatChoreographyGraphRootBranch(c.Run)
	batch := &endpointBatch{mailbox: c.canonicalMailbox, nodes: []string{c.Local.Name},
		refs: map[string]string{branch: oid}}
	if err := c.createInitialRefs(ctx, c.CanonicalEndpoint, batch); err != nil {
		return graphCarrierMissingIdentity, err
	}
	return oid, nil
}

// ReadChoreographyGraphCarrier performs one cold verification of the complete
// enumeration and every canonical task root before indexed pickup begins.
func (m *GitMailbox) ReadChoreographyGraphCarrier(ctx context.Context,
	binding ChoreographyRunBinding, bootstrapOID string, maxBytes int64) (ChoreographyGraphCarrier, string, error) {
	if m == nil || !binding.valid() || bootstrapOID == graphCarrierMissingIdentity ||
		maxBytes <= graphCarrierNoManifestBytes {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, fmt.Errorf(graphRootReadIdentityErrorMessage)
	}
	branch := FormatChoreographyGraphRootBranch(binding.Run)
	oid, err := m.readRemoteTip(ctx, branch)
	if err != nil {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, err
	}
	if oid == graphCarrierMissingIdentity {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, fmt.Errorf(graphRootAbsentErrorMessage)
	}
	if err := m.fetchRefs(ctx, []string{branch}); err != nil {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, err
	}
	parents, err := m.plumbing.CommitParents(ctx, oid)
	if err != nil {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, err
	}
	if len(parents) != graphCarrierSingleParentCount ||
		parents[graphCarrierFirstParentIndex] != bootstrapOID {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, fmt.Errorf(graphRootBootstrapParentErrorMessage)
	}
	tree, err := m.readTree(ctx, oid)
	if err != nil {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, err
	}
	if tree.kind != choreographyGraphRootKind {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, fmt.Errorf(graphRootSignedContentErrorMessage)
	}
	document, err := m.Read(ctx, ChainTip{OID: oid, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, maxBytes)
	if err != nil {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, err
	}
	var carrier ChoreographyGraphCarrier
	if err := strictJSON(document, &carrier); err != nil {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, err
	}
	if carrier.ChoreographyRunBinding != binding || carrier.BootstrapOID != bootstrapOID {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, fmt.Errorf(graphRootRunMismatchErrorMessage)
	}
	if err := validateChoreographyGraphCarrier(carrier); err != nil {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, err
	}
	if err := m.verifyChoreographyInitializationComplete(ctx, binding, bootstrapOID, oid); err != nil {
		return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, err
	}
	pattern := choreographyGraphRefNamespace + reservationBranchPrefix + binding.Run +
		choreographyGraphTaskRefPatternSuffix
	expected := make(map[string]ChoreographyTaskSpec)
	for _, task := range carrier.Tasks {
		if !task.Virtual {
			expected[task.Branch] = task
		}
	}
	for attempt := graphCarrierFirstReadAttempt; attempt < graphRootCoherentReadAttempts; attempt++ {
		heads, err := m.remote.ListRemoteHeads(ctx, m.endpoint, pattern)
		if err != nil {
			return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, err
		}
		if len(heads) != len(expected) {
			return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, fmt.Errorf(graphRootTaskRefCountErrorMessage)
		}
		ledgerTips, err := m.readChoreographyTaskLedgerTips(ctx, choreographyTaskLedgerProjection{
			Binding: binding, BootstrapOID: bootstrapOID, GraphRootOID: oid,
			Tasks: carrier.Tasks})
		if err != nil {
			return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, err
		}
		coherent := graphCarrierReadCoherent
		for _, head := range heads {
			task := expected[head.Name]
			if task.Task == graphCarrierMissingIdentity {
				return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, fmt.Errorf(graphRootAdditionalTaskRefErrorFormat, head.Name)
			}
			root, err := m.ReadChoreographyTaskRoot(ctx, ChoreographyTaskRootQuery{
				Binding: binding, Task: task.Task, Kind: task.Kind, BootstrapOID: bootstrapOID,
				MaxAttempts: task.EffectiveMaxAttempts()})
			if err != nil {
				return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, err
			}
			if root != task.ReservationOID {
				return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, fmt.Errorf(graphRootTaskRootMismatchErrorFormat, head.Name)
			}
			if head.OID != ledgerTips[task.Task] {
				coherent = graphCarrierReadIncoherent
			}
		}
		if coherent {
			return carrier, oid, nil
		}
	}
	return ChoreographyGraphCarrier{}, graphCarrierMissingIdentity, fmt.Errorf(choreographyTaskLedgerAncestryError)
}

func (m *GitMailbox) verifyChoreographyInitializationComplete(ctx context.Context,
	binding ChoreographyRunBinding, bootstrapOID, graphRootOID string) error {
	ledgerBranch := FormatChoreographyLedgerBranch(binding.Run)
	tip, err := m.readRemoteTip(ctx, ledgerBranch)
	if err != nil {
		return err
	}
	if tip == graphCarrierMissingIdentity {
		return fmt.Errorf(graphRootInitLedgerAbsentErrorMessage)
	}
	if err := m.fetchRefs(ctx, []string{ledgerBranch}); err != nil {
		return err
	}
	history, err := m.plumbing.FirstParentOIDs(ctx, tip, bootstrapOID)
	if err != nil {
		return err
	}
	if len(history) < graphCarrierInitHistoryMinimum {
		return fmt.Errorf(graphRootInitEventAbsentErrorMessage)
	}
	rootParents, err := m.plumbing.CommitParents(ctx, history[graphCarrierFirstParentIndex])
	if err != nil {
		return err
	}
	if len(rootParents) != graphCarrierSingleParentCount ||
		rootParents[graphCarrierFirstParentIndex] != bootstrapOID {
		return fmt.Errorf(graphRootLedgerParentErrorMessage)
	}
	eventParents, err := m.plumbing.CommitParents(ctx, history[graphCarrierInitEventIndex])
	if err != nil {
		return err
	}
	if len(eventParents) != graphCarrierInitParentCount ||
		eventParents[graphCarrierFirstParentIndex] != history[graphCarrierFirstParentIndex] ||
		eventParents[graphCarrierInitEventIndex] != graphRootOID {
		return fmt.Errorf(graphRootInitParentErrorMessage)
	}
	tree, err := m.readTree(ctx, history[graphCarrierInitEventIndex])
	if err != nil {
		return err
	}
	if tree.kind != choreographyLedgerKind {
		return fmt.Errorf(graphRootInitKindErrorMessage)
	}
	document, err := m.Read(ctx, ChainTip{OID: history[graphCarrierInitEventIndex], Kind: tree.kind,
		document: tree.document, signature: tree.signature}, returnReplyLimit)
	if err != nil {
		return err
	}
	var record choreographyLedgerRecord
	if err := strictJSON(document, &record); err != nil {
		return err
	}
	if record.ChoreographyRunBinding != binding || record.Sequence != graphCarrierInitSequence ||
		record.Event == nil || record.Event.Kind != ChoreographyInitializationComplete ||
		record.Event.Sequence != graphCarrierInitSequence ||
		record.Event.Task != choreographyRunTaskID || record.Event.TaskOID != graphRootOID ||
		record.Event.GraphRootOID != graphRootOID {
		return fmt.Errorf(graphRootInitIdentityErrorMessage)
	}
	return nil
}
