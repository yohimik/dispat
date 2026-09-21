// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

// One package's contribution to the plan digest: the policy that decides what
// it releases and which commands release it, the versions planning computed,
// and the units behind them.
//
// The split from digest.go is by subject rather than by size. That file owns
// the document and the rules the whole document obeys; this one owns the
// per-package reading of a resolved package and its Release, which is where
// the in-or-out decision has to be made field by field.

import (
	"sort"
	"strconv"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

type digestPackage struct {
	Name       string `json:"name"`
	Repository string `json:"repository"`
	Folder     string `json:"folder"`
	Source     string `json:"source"`

	Space          string `json:"space"`
	Versioning     string `json:"versioning"`
	VersionGroup   string `json:"versionGroup"`
	CounterSharing string `json:"counterSharing"`
	ChannelSharing string `json:"channelSharing"`

	TagFormat      string              `json:"tagFormat"`
	AliasFormats   []digestAliasFormat `json:"aliasFormats"`
	Commands       digestCommands      `json:"commands"`
	Env            []string            `json:"env"`
	BuildOutputs   []string            `json:"buildOutputs"`
	BuildPlatforms []string            `json:"buildPlatforms"`
	AutoVersion    *digestAutoVersion  `json:"autoVersion"`

	Current        string `json:"current"`
	Baseline       string `json:"baseline"`
	HasBaseline    bool   `json:"hasBaseline"`
	IsTagged       bool   `json:"tagged"`
	IsFromInitials bool   `json:"fromInitials"`
	StableCommit   string `json:"stableCommit"`
	BaselineCommit string `json:"baselineCommit"`

	OwnBump         string `json:"ownBump"`
	PropagatedBump  string `json:"propagatedBump"`
	Bump            string `json:"bump"`
	Channel         string `json:"channel"`
	BaselineChannel string `json:"baselineChannel"`
	ChannelFrom     string `json:"channelFrom"`
	Next            string `json:"next"`

	HasNewWork    bool `json:"newWork"`
	IsHeld        bool `json:"held"`
	IsPinned      bool `json:"pinned"`
	IsDeselected  bool `json:"deselected"`
	IsCatchUp     bool `json:"catchUp"`
	IsChannelOnly bool `json:"channelOnly"`
	IsFixedRide   bool `json:"fixedRide"`

	DueTo   []string       `json:"dueTo"`
	Updates []digestUpdate `json:"updates"`

	TagName   string            `json:"tagName"`
	AliasTags []digestAliasTag  `json:"aliasTags"`
	Units     []digestUnit      `json:"units"`
	Fresh     []digestUnit      `json:"freshUnits"`
	Corrects  []digestCorrected `json:"corrects"`
}

// digestCommands is every resolved command sequence a release of the package
// would run, hooks included. The command TEXT is semantic: a package built by
// a different command is a different plan even when every version in it is
// the same, which is exactly what a worker executing the plan has to agree
// about.
type digestCommands struct {
	Version   []string `json:"version"`
	Build     []string `json:"build"`
	Publish   []string `json:"publish"`
	Login     []string `json:"login"`
	Announce  []string `json:"announce"`
	BeforeAll []string `json:"beforeAll"`

	BeforeVersion  []string `json:"beforeVersion"`
	PostVersion    []string `json:"postVersion"`
	BeforeBuild    []string `json:"beforeBuild"`
	PostBuild      []string `json:"postBuild"`
	BeforePublish  []string `json:"beforePublish"`
	PostPublish    []string `json:"postPublish"`
	BeforeAnnounce []string `json:"beforeAnnounce"`
	PostAnnounce   []string `json:"postAnnounce"`

	OnFail []string `json:"onFail"`
	OnSkip []string `json:"onSkip"`
}

type digestAliasFormat struct {
	Format   string   `json:"format"`
	IsMoving bool     `json:"moving"`
	IsForced bool     `json:"force"`
	Channels []string `json:"channels"`
}

type digestAliasTag struct {
	Name     string `json:"name"`
	IsForced bool   `json:"force"`
}

type digestUpdate struct {
	Name string `json:"name"`
	From string `json:"from"`
	To   string `json:"to"`
	Tag  string `json:"tag"`
}

// digestCorrected is the record a unit restates (§7.4, §13.10), carried
// because a correction changes what the release publishes about itself and
// nothing else in the document says so.
type digestCorrected struct {
	Commit  string   `json:"commit"`
	Index   int      `json:"index"`
	Targets []string `json:"targets"`
}

// digestAutoVersion is the resolved manifest-rewriting policy. It decides
// which declarations the version stage writes and which commands refresh the
// lockfile afterwards, so it changes what a release produces.
type digestAutoVersion struct {
	Manifests      string              `json:"manifests"`
	Replace        []digestReplaceRule `json:"replace"`
	Kinds          []string            `json:"kinds"`
	Only           []string            `json:"only"`
	IsOnlyUpdated  bool                `json:"onlyUpdated"`
	IsNameMatching bool                `json:"nameSubstring"`
	Match          []string            `json:"match"`
	Range          string              `json:"range"`
	IsWritingOwn   bool                `json:"writeVersion"`
	SyncLock       []string            `json:"syncLock"`
}

type digestReplaceRule struct {
	Files []string `json:"files"`
	Find  string   `json:"find"`
	Write string   `json:"write"`
}

// canonicalPackage reads one planned package into the document.
//
// The absent fields are as deliberate as the present ones. Build and publish
// weights, the space's build-waits-publish ordering and its revert policy say
// how the run is scheduled and what it does about a failure, not what it
// releases; Outputs are exported at run time; Sources, WaitingFor and the
// author maps are derived from DueTo, the deselection marks and the commits
// already recorded here; and the package's absolute folder never appears,
// only its position inside its own repository.
func canonicalPackage(name string, release *Release, root string) digestPackage {
	pkg, space := release.Pkg, release.Pkg.Space
	base := pkg.RepoRoot
	if base == "" {
		base = root
	}
	out := digestPackage{
		Name:           name,
		Repository:     pkg.Repository,
		Folder:         relativeFolder(base, pkg.Dir),
		Source:         pkg.Src,
		Space:          space.Name,
		Versioning:     string(space.Versioning),
		VersionGroup:   pkg.VersionGroupIdentity(),
		CounterSharing: string(space.CounterSharing),
		ChannelSharing: string(space.ChannelSharing),
		TagFormat:      string(release.TagFormat()),
		AliasFormats:   canonicalAliasFormats(space.AliasTags),
		Commands:       canonicalCommands(space),
		Env:            append([]string(nil), space.Env...),
		BuildOutputs:   append([]string(nil), space.BuildOutputs...),
		BuildPlatforms: append([]string(nil), space.BuildPlatforms...),
		AutoVersion:    canonicalAutoVersion(space.AutoVersion),

		Current:        release.Current.String(),
		Baseline:       release.Baseline.String(),
		HasBaseline:    release.HasBaseline,
		IsTagged:       release.Tagged,
		IsFromInitials: release.FromInitials,
		StableCommit:   release.StableCommit,
		BaselineCommit: release.BaselineCommit,

		OwnBump:         release.OwnBump.String(),
		PropagatedBump:  release.PropagatedBump.String(),
		Bump:            release.Bump.String(),
		Channel:         release.Channel,
		BaselineChannel: release.BaselineChannel,
		ChannelFrom:     release.ChannelFrom,
		Next:            release.Next.String(),

		HasNewWork:    release.NewWork,
		IsHeld:        release.Held,
		IsPinned:      release.Pinned,
		IsDeselected:  release.Deselected,
		IsCatchUp:     release.CatchUp,
		IsChannelOnly: release.ChannelOnly,
		IsFixedRide:   release.FixedRide,

		DueTo:   sortedCopy(release.DueTo),
		Updates: canonicalUpdates(release.Updates),
		TagName: release.TagName(),
		Units:   canonicalUnits(release, release.Units),
		Fresh:   canonicalUnits(release, release.FreshUnits),
	}
	for _, alias := range release.AliasTags() {
		out.AliasTags = append(out.AliasTags, digestAliasTag{Name: alias.Name, IsForced: alias.Force})
	}
	out.Corrects = canonicalCorrects(release)
	return out
}

func canonicalCommands(space *model.Space) digestCommands {
	return digestCommands{
		Version:        append([]string(nil), space.VersionScript...),
		Build:          append([]string(nil), space.BuildScript...),
		Publish:        append([]string(nil), space.PublishScript...),
		Login:          append([]string(nil), space.LoginScript...),
		Announce:       append([]string(nil), space.AnnounceScript...),
		BeforeAll:      append([]string(nil), space.BeforeAllScript...),
		BeforeVersion:  append([]string(nil), space.BeforeVersionScript...),
		PostVersion:    append([]string(nil), space.PostVersionScript...),
		BeforeBuild:    append([]string(nil), space.BeforeBuildScript...),
		PostBuild:      append([]string(nil), space.PostBuildScript...),
		BeforePublish:  append([]string(nil), space.BeforePublishScript...),
		PostPublish:    append([]string(nil), space.PostPublishScript...),
		BeforeAnnounce: append([]string(nil), space.BeforeAnnounceScript...),
		PostAnnounce:   append([]string(nil), space.PostAnnounceScript...),
		OnFail:         append([]string(nil), space.OnFailScript...),
		OnSkip:         append([]string(nil), space.OnSkipScript...),
	}
}

func canonicalAliasFormats(aliases []model.AliasTag) []digestAliasFormat {
	out := make([]digestAliasFormat, 0, len(aliases))
	for _, alias := range aliases {
		out = append(out, digestAliasFormat{
			Format: alias.Format, IsMoving: alias.Moving, IsForced: alias.Force,
			Channels: sortedCopy(alias.Channels),
		})
	}
	return out
}

func canonicalUpdates(updates []ProviderUpdate) []digestUpdate {
	out := make([]digestUpdate, 0, len(updates))
	for _, update := range updates {
		out = append(out, digestUpdate{
			Name: update.Name, From: update.From.String(), To: update.To.String(), Tag: update.Tag,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// canonicalAutoVersion flattens the policy, sorting the two sets it carries.
// They are maps, and a map is the one thing this document never serializes.
func canonicalAutoVersion(policy *model.AutoVersion) *digestAutoVersion {
	if policy == nil {
		return nil
	}
	out := &digestAutoVersion{
		Manifests:      string(policy.Manifests),
		IsOnlyUpdated:  policy.OnlyUpdated,
		IsNameMatching: policy.NameSubstring,
		Match:          append([]string(nil), policy.Match...),
		Range:          policy.Range,
		IsWritingOwn:   policy.WriteVersion,
		SyncLock:       append([]string(nil), policy.SyncLock...),
	}
	for _, rule := range policy.Replace {
		out.Replace = append(out.Replace, digestReplaceRule{
			Files: append([]string(nil), rule.Files...), Find: rule.Find, Write: rule.Write,
		})
	}
	for kind, isTraversed := range policy.Kinds {
		if isTraversed {
			out.Kinds = append(out.Kinds, kind.String())
		}
	}
	for provider := range policy.Only {
		out.Only = append(out.Only, provider)
	}
	sort.Strings(out.Kinds)
	sort.Strings(out.Only)
	return out
}

// canonicalCorrects records what each unit restates, keyed the way the rest
// of the document keys a unit: by its commit and its position in the message.
// The map itself is keyed by unit pointer and is read through the release's
// own units in plan order, never ranged over.
func canonicalCorrects(release *Release) []digestCorrected {
	out := make([]digestCorrected, 0, len(release.Corrects))
	for _, unit := range release.Units {
		targets := release.Corrects[unit]
		if len(targets) == 0 {
			continue
		}
		out = append(out, digestCorrected{
			Commit: release.UnitCommits[unit], Index: unit.Index, Targets: sortedCopy(targets),
		})
	}
	return out
}

// digestUnit is one surviving unit: where it was written, what it is, and the
// directives that decide what it does to the plan. The description and the
// body are not here, and they do not have to be: a message is part of its
// commit, so an edited one is a different commit and a different head.
type digestUnit struct {
	Commit     string           `json:"commit"`
	Index      int              `json:"index"`
	Type       string           `json:"type"`
	Bump       string           `json:"bump"`
	TypeBump   string           `json:"typeBump"`
	IsBreaking bool             `json:"breaking"`
	IsValid    bool             `json:"valid"`
	Directives digestDirectives `json:"directives"`
}

type digestDirectives struct {
	Propagate             string   `json:"propagate"`
	IsPropagateSet        bool     `json:"propagateSet"`
	Depth                 string   `json:"depth"`
	IsDepthSet            bool     `json:"depthSet"`
	PropagateScope        string   `json:"propagateScope"`
	IsPropagateScopeSet   bool     `json:"propagateScopeSet"`
	PropagateChannel      string   `json:"propagateChannel"`
	IsChannelPropagateSet bool     `json:"propagateChannelSet"`
	ChannelDepth          string   `json:"channelDepth"`
	IsChannelDepthSet     bool     `json:"channelDepthSet"`
	ChannelScope          string   `json:"propagateChannelScope"`
	IsChannelScopeSet     bool     `json:"propagateChannelScopeSet"`
	Channel               string   `json:"channel"`
	IsChannelSet          bool     `json:"channelSet"`
	ReleaseAs             string   `json:"releaseAs"`
	Kinds                 []string `json:"kinds"`
	Edits                 []string `json:"edits"`
	Deletes               []string `json:"deletes"`
}

// canonicalUnits flattens a unit list in plan order, which is the order the
// planner put them in and the order §11.6 gives meaning to.
func canonicalUnits(release *Release, units []*ccme.Unit) []digestUnit {
	out := make([]digestUnit, 0, len(units))
	for _, unit := range units {
		out = append(out, digestUnit{
			Commit:     release.UnitCommits[unit],
			Index:      unit.Index,
			Type:       unit.Header.Type,
			Bump:       unit.Bump.String(),
			TypeBump:   unit.TypeBump.String(),
			IsBreaking: unit.Breaking,
			IsValid:    unit.Valid,
			Directives: canonicalDirectives(unit.Directives),
		})
	}
	return out
}

func canonicalDirectives(directives ccme.Directives) digestDirectives {
	out := digestDirectives{
		Propagate:             string(directives.Propagate),
		IsPropagateSet:        directives.PropagateSet,
		Depth:                 strconv.Itoa(int(directives.Depth)),
		IsDepthSet:            directives.DepthSet,
		PropagateScope:        directives.PropagateScope.String(),
		IsPropagateScopeSet:   directives.PropagateScopeSet,
		PropagateChannel:      directives.PropagateChannel.String(),
		IsChannelPropagateSet: directives.PropagateChannelSet,
		ChannelDepth:          strconv.Itoa(int(directives.ChannelDepth)),
		IsChannelDepthSet:     directives.ChannelDepthSet,
		ChannelScope:          directives.PropagateChannelScope.String(),
		IsChannelScopeSet:     directives.PropagateChannelScopeSet,
		Channel:               directives.Channel.String(),
		IsChannelSet:          directives.ChannelSet,
	}
	if directives.ReleaseAs != nil {
		out.ReleaseAs = directives.ReleaseAs.Raw
	}
	for _, kind := range directives.Kinds {
		out.Kinds = append(out.Kinds, string(kind))
	}
	for _, target := range directives.Edits {
		out.Edits = append(out.Edits, target.Raw)
	}
	for _, target := range directives.Deletes {
		out.Deletes = append(out.Deletes, target.Raw)
	}
	return out
}
