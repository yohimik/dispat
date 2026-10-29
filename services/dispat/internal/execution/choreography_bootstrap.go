// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const choreographyBootstrapKind MessageKind = "choreography-bootstrap"
const ChoreographyProtocolVersion = 3

// ObjectStoreDir identifies the verified local cache holding fetched
// bootstrap carriers. It is an input to a private planner, not a remote ref.
func (m *GitMailbox) ObjectStoreDir() string {
	if m == nil || m.plumbing == nil {
		return bootstrapAbsentIdentityText
	}
	return m.plumbing.Dir
}

// ChoreographyTagPackCarrier binds one repository's exact noncommit planning
// object closure to a bounded pack blob in the signed bootstrap tree. Commit
// target histories travel as direct parents instead of being duplicated here.
type ChoreographyTagPackCarrier struct {
	Repository string `json:"repository"`
	BlobOID    string `json:"blobOid"`
}

// RunBootstrap is the retained cold-start identity. Task reservations and
// the ledger root directly parent its commit, so probe-ref cleanup cannot
// remove the return pin or pinned planning envelope for this generation.
type RunBootstrap struct {
	Protocol int `json:"protocol"`
	ChoreographyRunBinding
	Mode                 string                       `json:"mode"`
	AlgorithmVersion     string                       `json:"algorithmVersion"`
	JoinPolicy           ChoreographyJoinPolicy       `json:"joinPolicy"`
	CoordinationEndpoint string                       `json:"coordinationEndpoint"`
	ReturnEndpoint       string                       `json:"returnEndpoint"`
	ReturnCertSHA256     string                       `json:"returnCertSha256"`
	PinnedInputs         PinnedRunInputs              `json:"pinnedInputs"`
	TagClosurePacks      []ChoreographyTagPackCarrier `json:"tagClosurePacks,omitempty"`
}

type ChoreographyBootstrapOptions struct {
	Bootstrap        *RunBootstrap
	MaxDocumentBytes int64
	MaxTagBytes      int64
	TagSources       []ChoreographyTagSource
	SinceSourceDir   string
}

// ChoreographyTagSource is local capture metadata for every pinned tag ref,
// including lightweight tags. SourceDir never appears in the signed
// bootstrap; only exact ref identities and closure-pack blob OIDs do.
type ChoreographyTagSource struct {
	Repository string
	Name       string
	Object     string
	Peeled     string
	SourceDir  string
}

// PrepareChoreographyBootstrap writes one signed commit locally. Source
// snapshots and pinned commit tag targets are direct parents; the bounded
// closure packs carry annotated tag chains and noncommit objects in the tree.
// It does not create or advance any remote ref.
func (m *GitMailbox) PrepareChoreographyBootstrap(ctx context.Context,
	opts ChoreographyBootstrapOptions) (string, error) {
	if m == nil || opts.Bootstrap == nil || opts.Bootstrap.Protocol != ChoreographyProtocolVersion ||
		!validChoreographyMode(opts.Bootstrap.Mode) ||
		!opts.Bootstrap.ChoreographyRunBinding.valid() || opts.MaxDocumentBytes <= bootstrapNoObjectBytes ||
		opts.MaxTagBytes <= bootstrapNoObjectBytes || len(opts.Bootstrap.PinnedInputs.Repositories) == bootstrapNoEntriesCount ||
		len(opts.Bootstrap.PinnedInputs.Selection) == bootstrapNoEntriesCount || len(opts.Bootstrap.PinnedInputs.Options) == bootstrapNoEntriesCount ||
		len(opts.Bootstrap.TagClosurePacks) != bootstrapNoEntriesCount {
		return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapIncompleteErrorText)
	}
	bootstrap := *opts.Bootstrap
	if err := gitx.RequireTransportEndpoint(bootstrap.CoordinationEndpoint); err != nil {
		return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapCoordinationEndpointInvalidErrorFormat, err)
	}
	if bootstrap.AlgorithmVersion == bootstrapAbsentIdentityText {
		return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapAlgorithmVersionMissingErrorText)
	}
	if err := bootstrap.JoinPolicy.Validate(bootstrap.CoordinationEndpoint); err != nil {
		return bootstrapAbsentIdentityText, err
	}
	if !validChoreographyBootstrapAttemptPolicy(bootstrap) {
		return bootstrapAbsentIdentityText, fmt.Errorf(choreographyRetryPolicyMismatchError)
	}
	endpoint, err := url.Parse(bootstrap.ReturnEndpoint)
	if err != nil || endpoint.Scheme != bootstrapReturnSchemeName || endpoint.Host == bootstrapAbsentIdentityText || endpoint.User != nil ||
		endpoint.RawQuery != bootstrapAbsentIdentityText || endpoint.Fragment != bootstrapAbsentIdentityText || endpoint.Path != bootstrapAbsentIdentityText && endpoint.Path != bootstrapReturnRootPath {
		return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapReturnEndpointInvalidErrorText)
	}
	pin, err := hex.DecodeString(bootstrap.ReturnCertSHA256)
	if err != nil || len(pin) != sha256.Size || bootstrap.ReturnCertSHA256 != hex.EncodeToString(pin) {
		return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapTlsPinInvalidErrorText)
	}
	parents := make([]string, bootstrapInitialEntryCount, len(bootstrap.PinnedInputs.Repositories))
	seenSources := map[string]bool{}
	for _, source := range bootstrap.PinnedInputs.Repositories {
		if (source.Name == bootstrapAbsentIdentityText && len(bootstrap.PinnedInputs.Repositories) != bootstrapSingleRepositoryCount) ||
			source.Snapshot == bootstrapAbsentIdentityText || source.PlannedHead == bootstrapAbsentIdentityText ||
			seenSources[source.Name] {
			return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapSourceInvalidErrorText)
		}
		seenSources[source.Name] = bootstrapInventoryItemPresentState
		parents = append(parents, source.Snapshot)
	}
	if anchor := bootstrap.PinnedInputs.SinceAnchor; anchor != nil {
		if !seenSources[anchor.Repository] || anchor.OID == bootstrapAbsentIdentityText || opts.SinceSourceDir == bootstrapAbsentIdentityText {
			return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapSinceAnchorIncompleteErrorText)
		}
		if err := m.plumbing.ImportCommitFrom(ctx, opts.SinceSourceDir, anchor.OID); err != nil {
			return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapSinceAnchorImportErrorFormat, err)
		}
		parents = append(parents, anchor.OID)
	} else if opts.SinceSourceDir != bootstrapAbsentIdentityText {
		return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapSinceAnchorUnexpectedErrorText)
	}
	refIndex := map[string]PinnedNativeRef{}
	for _, ref := range bootstrap.PinnedInputs.NativeRefs {
		key := ref.Repository + bootstrapNativeRefKeySeparatorText + ref.Name
		if !seenSources[ref.Repository] || ref.Name == bootstrapAbsentIdentityText || ref.Object == bootstrapAbsentIdentityText || refIndex[key].Name != bootstrapAbsentIdentityText {
			return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapNativeRefInvalidErrorText)
		}
		refIndex[key] = ref
	}
	carried := make([]gitx.TreeEntry, bootstrapInitialEntryCount, bootstrapInitialTreeCapacity)
	if len(refIndex) > bootstrapNoEntriesCount {
		sources := slices.Clone(opts.TagSources)
		if len(sources) != len(refIndex) {
			return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapTagSourceInventoryIncompleteErrorText)
		}
		slices.SortFunc(sources, func(a, b ChoreographyTagSource) int {
			return strings.Compare(a.Repository+bootstrapNativeRefKeySeparatorText+a.Name, b.Repository+bootstrapNativeRefKeySeparatorText+b.Name)
		})
		seenTags := map[string]bool{}
		seenTargets := map[string]bool{}
		type closure struct {
			dir     string
			include []string
			exclude []string
		}
		closures := map[string]*closure{}
		remaining := opts.MaxTagBytes
		for _, tag := range sources {
			key := tag.Repository + bootstrapNativeRefKeySeparatorText + tag.Name
			ref := refIndex[key]
			if seenTags[key] || ref.Name == bootstrapAbsentIdentityText || ref.Object != tag.Object || ref.Peeled != tag.Peeled ||
				tag.SourceDir == bootstrapAbsentIdentityText {
				return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapTagSourceMismatchErrorText)
			}
			seenTags[key] = bootstrapInventoryItemPresentState
			sourceGit := &gitx.LocalGitx{Dir: tag.SourceDir, Log: m.plumbing.Log}
			kind, err := sourceGit.ObjectType(ctx, tag.Object)
			if err != nil {
				return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapTagInspectionErrorFormat, tag.Name, err)
			}
			target, err := sourceGit.PeelObject(ctx, tag.Object)
			if err != nil {
				return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapTagPeelErrorFormat, tag.Name, err)
			}
			if kind == bootstrapTagObjectTypeName && tag.Peeled != target || kind != bootstrapTagObjectTypeName && tag.Peeled != bootstrapAbsentIdentityText {
				return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapTagTargetMismatchErrorText)
			}
			targetType, err := sourceGit.ObjectType(ctx, target)
			if err != nil {
				return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapTagTargetUnavailableErrorFormat, tag.Name, err)
			}
			if targetType == bootstrapTagObjectTypeName {
				return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapTagTargetMissingErrorText)
			}
			group := closures[tag.Repository]
			if group == nil {
				group = &closure{dir: tag.SourceDir}
				closures[tag.Repository] = group
			} else if group.dir != tag.SourceDir {
				return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapMultipleTagStoresErrorText)
			}
			if targetType == bootstrapCommitObjectTypeName && !seenTargets[target] {
				// Historical release history may sit outside the source branch.
				if err := m.plumbing.ImportCommitFrom(ctx, tag.SourceDir, target); err != nil {
					return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapTagTargetImportErrorFormat, tag.Name, err)
				}
				seenTargets[target] = bootstrapInventoryItemPresentState
			}
			if targetType == bootstrapCommitObjectTypeName {
				parents = append(parents, target)
			}
			if kind != bootstrapCommitObjectTypeName {
				group.include = append(group.include, tag.Object)
				if targetType == bootstrapCommitObjectTypeName {
					group.exclude = append(group.exclude, target)
				}
			}
		}
		entries := make([]gitx.TreeEntry, bootstrapInitialEntryCount, len(closures))
		repos := make([]string, bootstrapInitialEntryCount, len(closures))
		for name := range closures {
			repos = append(repos, name)
		}
		slices.Sort(repos)
		for _, name := range repos {
			group := closures[name]
			if len(group.include) == bootstrapNoEntriesCount {
				continue
			}
			if remaining <= bootstrapNoObjectBytes {
				return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapTagClosureLimitErrorText)
			}
			blobOID, size, err := m.plumbing.ImportObjectClosurePackAsBlob(ctx, gitx.ObjectClosurePackImportOptions{
				SourceDir: group.dir, IncludeOIDs: group.include, ExcludeCommitOIDs: group.exclude, MaxBytes: remaining,
			})
			if err != nil {
				return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapPinnedObjectsCarryErrorFormat, name, err)
			}
			remaining -= size
			bootstrap.TagClosurePacks = append(bootstrap.TagClosurePacks,
				ChoreographyTagPackCarrier{Repository: name, BlobOID: blobOID})
			entries = append(entries, gitx.TreeEntry{Mode: gitx.TreeModeFile, Type: bootstrapBlobObjectTypeName,
				OID: blobOID, Name: bootstrapPackFilePrefixText + strconv.Itoa(len(entries))})
		}
		if len(entries) > bootstrapNoEntriesCount {
			plumbing := gitx.NewPlumbing(m.plumbing)
			tree := plumbing.MakeTree(ctx, entries)
			if err := plumbing.Err(); err != nil {
				return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapTagCarrierTreeWriteErrorFormat, err)
			}
			carried = append(carried, gitx.TreeEntry{Mode: gitx.TreeModeDir,
				Type: bootstrapTreeObjectTypeName, OID: tree, Name: bootstrapTagObjectsDirectoryName})
		}
	} else if len(opts.TagSources) != bootstrapNoEntriesCount {
		return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapTagSourceUnexpectedErrorText)
	}
	// The parent set is canonical even if multiple sources share a snapshot.
	slices.Sort(parents)
	parents = slices.Compact(parents)
	document, err := json.Marshal(bootstrap)
	if err != nil {
		return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapDocumentEncodingErrorFormat, err)
	}
	if int64(len(document)) > opts.MaxDocumentBytes {
		return bootstrapAbsentIdentityText, fmt.Errorf(bootstrapDocumentLimitErrorText)
	}
	return m.commitMessage(ctx, choreographyBootstrapKind, document, parents, carried)
}

// ReadChoreographyBootstrap authenticates the retained run identity and its
// complete direct-parent carrier set. It also reconstructs exact annotated
// tag objects from signed blob OIDs, so later private planning need not trust
// mutable tag refs. The caller must still compare the returned run binding
// and independently recomputed plan and graph digests.
func (m *GitMailbox) ReadChoreographyBootstrap(ctx context.Context, oid string,
	maxDocumentBytes, maxTagBytes int64) (RunBootstrap, error) {
	if m == nil || oid == bootstrapAbsentIdentityText || maxDocumentBytes <= bootstrapNoObjectBytes || maxTagBytes <= bootstrapNoObjectBytes {
		return RunBootstrap{}, fmt.Errorf(bootstrapReadIncompleteErrorText)
	}
	tree, err := m.readTree(ctx, oid)
	if err != nil {
		return RunBootstrap{}, err
	}
	if tree.kind != choreographyBootstrapKind {
		return RunBootstrap{}, &Rejection{Reason: ReasonChain}
	}
	document, err := m.Read(ctx, ChainTip{OID: oid, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, maxDocumentBytes)
	if err != nil {
		return RunBootstrap{}, err
	}
	var bootstrap RunBootstrap
	if err := json.Unmarshal(document, &bootstrap); err != nil ||
		bootstrap.Protocol != ChoreographyProtocolVersion ||
		!validChoreographyMode(bootstrap.Mode) ||
		!bootstrap.ChoreographyRunBinding.valid() ||
		len(bootstrap.PinnedInputs.Repositories) == bootstrapNoEntriesCount ||
		len(bootstrap.PinnedInputs.Selection) == bootstrapNoEntriesCount || len(bootstrap.PinnedInputs.Options) == bootstrapNoEntriesCount {
		return RunBootstrap{}, &Rejection{Reason: ReasonUnreadable}
	}
	if gitx.RequireTransportEndpoint(bootstrap.CoordinationEndpoint) != nil ||
		bootstrap.AlgorithmVersion == bootstrapAbsentIdentityText ||
		bootstrap.JoinPolicy.Validate(bootstrap.CoordinationEndpoint) != nil ||
		!validChoreographyBootstrapAttemptPolicy(bootstrap) {
		return RunBootstrap{}, &Rejection{Reason: ReasonChain}
	}
	endpoint, err := url.Parse(bootstrap.ReturnEndpoint)
	if err != nil || endpoint.Scheme != bootstrapReturnSchemeName || endpoint.Host == bootstrapAbsentIdentityText || endpoint.User != nil ||
		endpoint.RawQuery != bootstrapAbsentIdentityText || endpoint.Fragment != bootstrapAbsentIdentityText || endpoint.Path != bootstrapAbsentIdentityText && endpoint.Path != bootstrapReturnRootPath {
		return RunBootstrap{}, &Rejection{Reason: ReasonChain}
	}
	pin, err := hex.DecodeString(bootstrap.ReturnCertSHA256)
	if err != nil || len(pin) != sha256.Size || bootstrap.ReturnCertSHA256 != hex.EncodeToString(pin) {
		return RunBootstrap{}, &Rejection{Reason: ReasonChain}
	}
	parents := make([]string, bootstrapInitialEntryCount, len(bootstrap.PinnedInputs.Repositories)+len(bootstrap.PinnedInputs.NativeRefs))
	sources := make(map[string]bool, len(bootstrap.PinnedInputs.Repositories))
	for _, source := range bootstrap.PinnedInputs.Repositories {
		if (source.Name == bootstrapAbsentIdentityText && len(bootstrap.PinnedInputs.Repositories) != bootstrapSingleRepositoryCount) ||
			sources[source.Name] || source.Snapshot == bootstrapAbsentIdentityText || source.PlannedHead == bootstrapAbsentIdentityText {
			return RunBootstrap{}, &Rejection{Reason: ReasonChain}
		}
		sources[source.Name] = bootstrapInventoryItemPresentState
		snapshotParents, err := m.plumbing.CommitParents(ctx, source.Snapshot)
		if err != nil || len(snapshotParents) != bootstrapSnapshotParentCount || snapshotParents[bootstrapFirstEntryIndex] != source.PlannedHead {
			return RunBootstrap{}, &Rejection{Reason: ReasonChain}
		}
		parents = append(parents, source.Snapshot)
	}
	if anchor := bootstrap.PinnedInputs.SinceAnchor; anchor != nil {
		if !sources[anchor.Repository] || anchor.OID == bootstrapAbsentIdentityText {
			return RunBootstrap{}, &Rejection{Reason: ReasonChain}
		}
		if resolved, err := m.plumbing.ResolveCommit(ctx, anchor.OID); err != nil || resolved != anchor.OID {
			return RunBootstrap{}, &Rejection{Reason: ReasonChain}
		}
		parents = append(parents, anchor.OID)
	}
	refs := make(map[string]PinnedNativeRef, len(bootstrap.PinnedInputs.NativeRefs))
	refsByRepo := make(map[string]int, len(sources))
	for _, ref := range bootstrap.PinnedInputs.NativeRefs {
		key := ref.Repository + bootstrapNativeRefKeySeparatorText + ref.Name
		if !sources[ref.Repository] || ref.Name == bootstrapAbsentIdentityText || ref.Object == bootstrapAbsentIdentityText || refs[key].Name != bootstrapAbsentIdentityText {
			return RunBootstrap{}, &Rejection{Reason: ReasonChain}
		}
		refs[key] = ref
		refsByRepo[ref.Repository]++
	}
	expectedFiles := map[string]string{
		bootstrapDocumentPath:  tree.document,
		bootstrapSignaturePath: tree.signature,
	}
	packByRepo := make(map[string]string, len(bootstrap.TagClosurePacks))
	previousRepo := bootstrapAbsentIdentityText
	for index, pack := range bootstrap.TagClosurePacks {
		if pack.BlobOID == bootstrapAbsentIdentityText || !sources[pack.Repository] || refsByRepo[pack.Repository] == bootstrapNoEntriesCount ||
			packByRepo[pack.Repository] != bootstrapAbsentIdentityText || (index > bootstrapFirstEntryIndex && pack.Repository <= previousRepo) {
			return RunBootstrap{}, &Rejection{Reason: ReasonChain}
		}
		previousRepo = pack.Repository
		packByRepo[pack.Repository] = pack.BlobOID
		expectedFiles[bootstrapPackTreePathPrefix+strconv.Itoa(index)] = pack.BlobOID
	}
	plumbing := gitx.NewPlumbing(m.plumbing)
	plumbing.ListTree(ctx, oid, func(entry gitx.TreeEntry) error {
		if expectedFiles[entry.Name] != entry.OID || entry.Type != bootstrapBlobObjectTypeName {
			return fmt.Errorf(bootstrapTreeEntryUnexpectedErrorFormat, entry.Name)
		}
		delete(expectedFiles, entry.Name)
		return nil
	})
	if err := plumbing.Err(); err != nil || len(expectedFiles) != bootstrapNoEntriesCount {
		return RunBootstrap{}, &Rejection{Reason: ReasonChain}
	}
	remaining := maxTagBytes
	for _, pack := range bootstrap.TagClosurePacks {
		if remaining <= bootstrapNoObjectBytes {
			return RunBootstrap{}, &Rejection{Reason: ReasonOversize}
		}
		size, err := m.plumbing.RestoreObjectClosurePackFromBlob(ctx, pack.BlobOID, remaining)
		if err != nil {
			return RunBootstrap{}, fmt.Errorf(bootstrapPinnedObjectsRestoreErrorFormat, pack.Repository, err)
		}
		remaining -= size
	}
	for _, ref := range bootstrap.PinnedInputs.NativeRefs {
		kind, err := m.plumbing.ObjectType(ctx, ref.Object)
		if err != nil {
			return RunBootstrap{}, &Rejection{Reason: ReasonChain}
		}
		if kind != bootstrapCommitObjectTypeName && packByRepo[ref.Repository] == bootstrapAbsentIdentityText {
			return RunBootstrap{}, &Rejection{Reason: ReasonChain}
		}
		target, err := m.plumbing.PeelObject(ctx, ref.Object)
		if err != nil {
			return RunBootstrap{}, &Rejection{Reason: ReasonChain}
		}
		if kind == bootstrapTagObjectTypeName && ref.Peeled != target || kind != bootstrapTagObjectTypeName && ref.Peeled != bootstrapAbsentIdentityText {
			return RunBootstrap{}, &Rejection{Reason: ReasonChain}
		}
		targetType, err := m.plumbing.ObjectType(ctx, target)
		if err != nil || targetType == bootstrapTagObjectTypeName {
			return RunBootstrap{}, &Rejection{Reason: ReasonChain}
		}
		if targetType == bootstrapCommitObjectTypeName {
			parents = append(parents, target)
		}
	}
	slices.Sort(parents)
	parents = slices.Compact(parents)
	actualParents, err := m.plumbing.CommitParents(ctx, oid)
	if err != nil || !slices.Equal(parents, actualParents) {
		return RunBootstrap{}, &Rejection{Reason: ReasonChain}
	}
	return bootstrap, nil
}

func validChoreographyMode(mode string) bool {
	return mode == bootstrapCompilationModeName || mode == bootstrapInterpretationModeName || mode == bootstrapHybridModeName
}

func validChoreographyBootstrapAttemptPolicy(bootstrap RunBootstrap) bool {
	if bootstrap.JoinPolicy.DistributionMode != bootstrapAbsentIdentityText || bootstrap.Mode == bootstrapHybridModeName {
		return bootstrap.JoinPolicy.DistributionMode == bootstrap.Mode &&
			validChoreographyAttemptLimit(bootstrap.JoinPolicy.MaxAttempts)
	}
	if bootstrap.Mode == bootstrapInterpretationModeName {
		return validChoreographyAttemptLimit(bootstrap.JoinPolicy.MaxAttempts)
	}
	return bootstrap.JoinPolicy.MaxAttempts == choreographyAttemptPolicyLegacyUnspecified
}
