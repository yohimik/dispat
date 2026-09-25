// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

// The plan digest: one name for the semantic content of a plan.
//
// A distributed run has to be able to say that two nodes are working on the
// same release. CCME §28.3 asks for a documented canonical serialization of
// the semantic input and the plan content, with the transient execution
// identity bound separately, so that "the same plan" is a question about what
// is released rather than about which machine built it. §17.2 states the
// other half: placement settings, worker availability, run identifiers,
// branch names and completion order MUST NOT change the semantic plan.
//
// So the rule every field below is decided by is: it is part of the digest
// when it changes WHAT is released or WHICH commands produce it, and it is
// left out when it only changes HOW the run is observed or executed. The
// whole `execution` object is therefore absent, and so are the run id, the
// attempt, the branch names, the wall clock, the run-time outputs, the
// logging, the webhooks and every absolute filesystem path: two clones of one
// repository at different paths digest equally, which is what lets a worker
// check a plan it was handed against the one it can compute itself.
//
// Determinism is structural rather than hopeful. Nothing here serializes a
// map: every collection is a slice ordered by p.Order or by an explicit sort,
// and the unit-keyed maps a Release carries (Corrects, UnitAuthors,
// UnitCommits) are read through their keys in plan order rather than ranged
// over, because their keys are pointers whose iteration order is both
// unstable and meaningless.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// DigestSchema tags the canonical document, and it is the first field of it.
//
// Changing which fields the document carries, what they are called or how
// they are ordered changes every digest this version computes, which is
// exactly the kind of change a worker checking a plan against an orchestrator
// has to notice. The tag makes that visible instead of silent: bump it in the
// same commit as any change to the document, and a node running the older
// code disagrees loudly rather than agreeing by accident.
const DigestSchema = "dispat-plan-digest/2"

// DigestInput is what the digest needs and the plan does not carry: the head
// snapshot for a single history, the planner inputs the plan was computed
// from, and the invocation's explicit selection.
//
// It is a parameter rather than state on the Plan because the plan is the
// result of planning and these are its inputs; binding them here keeps the
// one thing §28.3 separates — semantic input and plan content — visible as
// two operands of one function.
type DigestInput struct {
	// Heads stands in for Plan.RepositoryHeads where planning left it empty,
	// which is every single-history plan: the caller passes {"": <full OID of
	// HEAD>}, the empty key being the legacy repository identity the rest of
	// the planner uses. It is ignored when the plan carries heads of its own.
	Heads map[string]string
	// Options are the planner inputs Compute was given. Only the semantic
	// ones are read — the dependency edges and the commit-message parser
	// configuration — because the rest is either already visible in the plan
	// content (the initials a baseline came from, the scopes a unit resolved
	// through, the baselines a window was cut at) or is not semantic at all
	// (the root path, the logger, the workload counters). The run's own
	// masked tags are deliberately among the ones left out: they are this
	// invocation's identity rather than the repository's, and a step command
	// inside a release must digest the release it belongs to.
	Options Options
	// Selection is the invocation's explicit release selection. A selection
	// changes what is released, so it belongs in the digest; the folder the
	// command was invoked from does not, because it is an absolute path and
	// what it selected is already visible in the plan's deselection marks.
	Selection DigestSelection
}

// DigestSelection is the explicit part of a release selection: the terms the
// command line named and the two refusals that turn an incomplete selection
// into no release at all.
type DigestSelection struct {
	Packages []string
	Spaces   []string
	Groups   []string
	// IsStrict is --strict: the selection is refused unless the plan can
	// release it cleanly.
	IsStrict bool
	// IsReleaseRequired is --require-release: a plan that publishes nothing
	// is refused.
	IsReleaseRequired bool
}

// CalculateDigest names this plan's semantic content: the lowercase hex
// SHA-256 of the canonical document described at the top of this file.
//
// Two runs of one repository at one state compute the same digest however
// they are placed, named, ordered or timed, and any change to what is
// released or to the commands that release it changes it. That is the whole
// contract; nothing reads the document itself.
func (p *Plan) CalculateDigest(input DigestInput) (string, error) {
	encoded, err := json.Marshal(p.canonicalDigest(input))
	if err != nil {
		return "", fmt.Errorf("serializing the canonical plan digest document: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// digestDocument is the canonical document. Every field is exported for
// encoding/json alone; the type and its children are unexported because the
// serialization is an implementation detail of the digest and nothing outside
// this file may come to depend on its shape.
type digestDocument struct {
	Schema    string                `json:"schema"`
	Heads     []digestHead          `json:"heads"`
	Packages  []digestPackage       `json:"packages"`
	Edges     []digestEdge          `json:"edges"`
	Providers []digestProviderEdges `json:"providers"`
	Parsers   []digestParser        `json:"parsers"`

	Diagnostics []digestDiagnostic `json:"diagnostics"`
	Selection   digestSelection    `json:"selection"`
}

type digestHead struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

type digestEdge struct {
	Consumer string `json:"consumer"`
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
}

type digestProviderEdges struct {
	Consumer  string   `json:"consumer"`
	Providers []string `json:"providers"`
}

type digestDiagnostic struct {
	Code       string `json:"code"`
	Package    string `json:"package"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

type digestSelection struct {
	Packages          []string `json:"packages"`
	Spaces            []string `json:"spaces"`
	Groups            []string `json:"groups"`
	IsStrict          bool     `json:"strict"`
	IsReleaseRequired bool     `json:"requireRelease"`
}

// canonicalDigest builds the whole document. It is one function because the
// document's field order is part of the schema and a reader checking what the
// digest covers should be able to read it in one place.
func (p *Plan) canonicalDigest(input DigestInput) digestDocument {
	document := digestDocument{
		Schema:      DigestSchema,
		Heads:       canonicalHeads(p.RepositoryHeads, input.Heads),
		Packages:    make([]digestPackage, 0, len(p.Order)),
		Edges:       canonicalEdges(input.Options.Dependencies),
		Providers:   canonicalProviders(p.Providers),
		Parsers:     canonicalParsers(input.Options),
		Diagnostics: canonicalDiagnostics(p.Diagnostics),
		Selection: digestSelection{
			Packages:          sortedCopy(input.Selection.Packages),
			Spaces:            sortedCopy(input.Selection.Spaces),
			Groups:            sortedCopy(input.Selection.Groups),
			IsStrict:          input.Selection.IsStrict,
			IsReleaseRequired: input.Selection.IsReleaseRequired,
		},
	}
	for _, name := range p.Order {
		document.Packages = append(document.Packages, canonicalPackage(name, p.Releases[name], input.Options.Root))
	}
	return document
}

// canonicalHeads renders the head snapshot planning read, falling back to the
// caller's for the single history the planner leaves empty.
func canonicalHeads(planned, supplied map[string]string) []digestHead {
	heads := planned
	if len(heads) == 0 {
		heads = supplied
	}
	out := make([]digestHead, 0, len(heads))
	for repository, commit := range heads {
		out = append(out, digestHead{Repository: repository, Commit: commit})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repository < out[j].Repository })
	return out
}

// canonicalEdges renders the dependency graph with the kind of each edge,
// because which manifest field an edge stands for decides whether it
// propagates at all (§8.4).
func canonicalEdges(dependencies []model.Dependency) []digestEdge {
	out := make([]digestEdge, 0, len(dependencies))
	for _, dependency := range dependencies {
		out = append(out, digestEdge{
			Consumer: dependency.Consumer,
			Provider: dependency.Provider,
			Kind:     dependency.Kind.String(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Consumer != out[j].Consumer {
			return out[i].Consumer < out[j].Consumer
		}
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// canonicalProviders renders the resolved provider sets. They are derivable
// from the edges for a workspace whose every edge is active, and they are not
// for one whose edges reach outside it, so the plan's own answer is recorded
// beside the declarations it came from.
func canonicalProviders(providers map[string][]string) []digestProviderEdges {
	out := make([]digestProviderEdges, 0, len(providers))
	for consumer, names := range providers {
		out = append(out, digestProviderEdges{Consumer: consumer, Providers: sortedCopy(names)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Consumer < out[j].Consumer })
	return out
}

// canonicalDiagnostics renders every diagnostic as its code and the place it
// was raised about. The message is left out on purpose: rewording a refusal
// is not a different plan, and a digest that changed with the prose would
// make every message edit a coordination failure.
func canonicalDiagnostics(diagnostics []Diagnostic) []digestDiagnostic {
	out := make([]digestDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		out = append(out, digestDiagnostic{
			Code:       diagnostic.Code,
			Package:    diagnostic.Pkg,
			Repository: diagnostic.Repository,
			Commit:     diagnostic.Commit,
		})
	}
	sort.Slice(out, func(i, j int) bool { return digestDiagnosticKey(out[i]) < digestDiagnosticKey(out[j]) })
	return out
}

// digestParser is one history's commit-message parser configuration: the
// grammar the plan's units were read under. Two nodes reading one repository
// with different separators, type tables or propagation defaults would derive
// different units from the same commits, so the configuration belongs in the
// digest beside the units it produced.
type digestParser struct {
	Repository           string            `json:"repository"`
	Separator            string            `json:"separator"`
	Types                []digestTypeBump  `json:"types"`
	IsStrictTypes        bool              `json:"strictTypes"`
	IsLenient            bool              `json:"lenient"`
	MaxDescriptionLength int               `json:"maxDescriptionLength"`
	Propagation          digestPropagation `json:"propagation"`
	Limits               digestLimits      `json:"limits"`
	AllowedChannels      []string          `json:"allowedChannels"`
	MessageLevelTrailers []string          `json:"messageLevelTrailers"`
	IssueTrailers        []string          `json:"issueTrailers"`
}

type digestTypeBump struct {
	Type string `json:"type"`
	Bump string `json:"bump"`
}

type digestPropagation struct {
	Bump         string   `json:"bump"`
	Depth        int      `json:"depth"`
	ChannelDepth int      `json:"channelDepth"`
	Channel      string   `json:"channel"`
	Kinds        []string `json:"kinds"`
}

type digestLimits struct {
	UnitsPerMessage   int `json:"unitsPerMessage"`
	ScopeTermsPerUnit int `json:"scopeTermsPerUnit"`
	MessageBytes      int `json:"messageBytes"`
}

// canonicalParsers renders the entry configuration's parser under the empty
// repository identity and every composed repository's under its own name, in
// name order, which is exactly how the planner keys them.
func canonicalParsers(options Options) []digestParser {
	out := []digestParser{canonicalParser("", options.ParserConfig)}
	for name, history := range options.Repositories {
		out = append(out, canonicalParser(name, history.ParserConfig))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repository < out[j].Repository })
	return out
}

func canonicalParser(repository string, config ccme.Config) digestParser {
	out := digestParser{
		Repository:           repository,
		Separator:            config.Separator,
		IsStrictTypes:        config.StrictTypes,
		IsLenient:            config.Lenient,
		MaxDescriptionLength: config.MaxDescriptionLength,
		Propagation: digestPropagation{
			Bump:         string(config.Propagation.Bump),
			Depth:        int(config.Propagation.Depth),
			ChannelDepth: int(config.Propagation.ChannelDepth),
			Channel:      config.Propagation.Channel,
		},
		Limits: digestLimits{
			UnitsPerMessage:   config.Limits.UnitsPerMessage,
			ScopeTermsPerUnit: config.Limits.ScopeTermsPerUnit,
			MessageBytes:      config.Limits.MessageBytes,
		},
		AllowedChannels:      sortedCopy(config.AllowedChannels),
		MessageLevelTrailers: sortedCopy(config.MessageLevelTrailers),
		IssueTrailers:        sortedCopy(config.IssueTrailers),
	}
	for _, kind := range config.Propagation.Kinds {
		out.Propagation.Kinds = append(out.Propagation.Kinds, string(kind))
	}
	sort.Strings(out.Propagation.Kinds)
	for name, bump := range config.Types {
		out.Types = append(out.Types, digestTypeBump{Type: name, Bump: bump.String()})
	}
	sort.Slice(out.Types, func(i, j int) bool { return out.Types[i].Type < out.Types[j].Type })
	return out
}

func digestDiagnosticKey(d digestDiagnostic) string {
	return strings.Join([]string{d.Code, d.Package, d.Repository, d.Commit}, "\x00")
}

// sortedCopy is the one ordering rule the digest's plain string lists share:
// the order terms were written in never changes what they select, so it must
// not change the digest either.
func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

// relativeFolder renders dir as a slash-separated path relative to the
// repository root that owns it, which is what makes two clones of one
// repository at different paths digest equally.
//
// Discovery only ever produces folders under their own repository root, so
// the prefix is always there to take off; a folder from anywhere else would
// simply keep the path it came with, and there is no failure for a digest to
// report about it.
func relativeFolder(base, dir string) string {
	return strings.TrimPrefix(filepath.ToSlash(dir), filepath.ToSlash(base)+"/")
}
