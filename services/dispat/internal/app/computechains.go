// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"fmt"
	"sort"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
)

const (
	chainInitialIndex                  = 0
	chainInitialDegreeSum              = 0
	chainNoMissingCheckoutCount        = 0
	chainMaximumNeighborCount          = 2
	chainEndpointDegreeLimit           = 2
	chainEdgeEndpointCount             = 2
	chainTreeEdgeDifferenceCount       = 1
	chainMinimumDisconnectedGroupCount = 2
	chainEmptyRepositoryName           = ""
	chainEmptyPeerURL                  = ""
	fleetLinkPairSeparator             = "\x00"
	isChainMemberSeen                  = true
	isFleetRosterMember                = true
	isFleetLinkRecorded                = true
	isOwnerPeerDeclared                = true
	isOwnerPeerUndeclared              = false
	chainBranchErrorFormat             = "minimal topology requires a chain: repository %s has %d neighbors; compute retains existing links and cannot remove this branch"
	chainCycleErrorMessage             = "minimal topology requires an acyclic chain; existing fleet links contain a cycle; compute never removes links"
	chainIncompleteErrorMessage        = "minimal chain is incomplete: no available endpoint checkout can join the remaining repositories; initialize the missing path endpoints before retrying"
)

// chainGroup is a path component, including a singleton. A singleton has one
// endpoint which can accept two links over successive repair rounds.
type chainGroup struct {
	ends       []rosterEntry
	isEntry    bool
	isComposed bool
}

// suggestChainLink preserves a linear forest and joins two of its components
// at free endpoints. One join at a time lets composition inspect the remote
// checkout before deciding where the path can safely grow next.
func (a *App) suggestChainLink(fleet []rosterEntry) ([]linkSuggestion, error) {
	adjacency := fleetLinkAdjacency(fleet, a.workspace.Repositories)
	byFold := make(map[string]rosterEntry, len(fleet))
	ordered := make([]string, chainInitialIndex, len(fleet))
	for _, member := range fleet {
		key := globx.Fold(member.name)
		byFold[key] = member
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		if len(adjacency[key]) > chainMaximumNeighborCount {
			return nil, fmt.Errorf(chainBranchErrorFormat, byFold[key].name, len(adjacency[key]))
		}
	}
	entry := chainEmptyRepositoryName
	if repository := a.workspace.EntryRepository(); repository != nil {
		entry = globx.Fold(repository.Name)
	}
	seen := make(map[string]bool, len(fleet))
	var groups []chainGroup
	for _, start := range ordered {
		if seen[start] {
			continue
		}
		group := chainGroup{}
		queue := []string{start}
		seen[start] = isChainMemberSeen
		degreeSum := chainInitialDegreeSum
		for i := chainInitialIndex; i < len(queue); i++ {
			current := queue[i]
			group.isEntry = group.isEntry || current == entry
			group.isComposed = group.isComposed || byFold[current].composed
			degreeSum += len(adjacency[current])
			if len(adjacency[current]) < chainEndpointDegreeLimit {
				group.ends = append(group.ends, byFold[current])
			}
			for _, peer := range adjacency[current] {
				if !seen[peer] {
					seen[peer] = isChainMemberSeen
					queue = append(queue, peer)
				}
			}
		}
		if degreeSum/chainEdgeEndpointCount != len(queue)-chainTreeEdgeDifferenceCount {
			return nil, fmt.Errorf(chainCycleErrorMessage)
		}
		sort.Slice(group.ends, func(i, j int) bool {
			if group.ends[i].composed != group.ends[j].composed {
				return group.ends[i].composed
			}
			return globx.Fold(group.ends[i].name) < globx.Fold(group.ends[j].name)
		})
		groups = append(groups, group)
	}
	if len(groups) < chainMinimumDisconnectedGroupCount {
		return nil, nil
	}
	// Existing but unread checkouts may reveal more edges or a branch. Read
	// them before choosing any new edge from the incomplete view.
	if len(a.missingCheckouts()) > chainNoMissingCheckoutCount {
		return nil, nil
	}
	first := chainInitialIndex
	for i, group := range groups {
		if group.isComposed {
			first = i
			break
		}
	}
	for i, group := range groups {
		if group.isEntry {
			first = i
			break
		}
	}
	for i, group := range groups {
		if i == first {
			continue
		}
		for _, from := range groups[first].ends {
			for _, to := range group.ends {
				owner, peer := chooseLinkOwner(from, to)
				if owner.composed {
					// Composition follows only roster-declared links. A URL
					// learned from a different peer is not yet a declaration
					// in this owner: repair its roster before staging a link
					// that recomposition would otherwise overlook.
					if peer.url != chainEmptyPeerURL && !a.ownerNamesPeer(owner.name, peer.name) {
						return nil, nil
					}
					return []linkSuggestion{a.proposeLink(owner, peer)}, nil
				}
			}
		}
	}
	return nil, fmt.Errorf(chainIncompleteErrorMessage)
}

// ownerNamesPeer checks the configuration that will own the new gitlink.
func (a *App) ownerNamesPeer(owner, peer string) bool {
	repository := a.workspace.RepositoryByName(owner)
	if repository == nil || repository.Config == nil {
		return isOwnerPeerUndeclared
	}
	for _, declared := range repository.Config.Repositories {
		if globx.Fold(declared.Name) == globx.Fold(peer) {
			return isOwnerPeerDeclared
		}
	}
	return isOwnerPeerUndeclared
}

// fleetLinkAdjacency maps every fleet identity, folded, to the identities its
// existing links join it to, in folded order.
//
// Both ends of a link may declare it, so each pair is recorded once from
// whichever end named it first, and the order is fixed so that a walk over it
// answers the same member whatever order the fleet was composed in. A link
// naming a repository the roster does not hold, which is what a disabled
// participant leaves behind, joins nothing: this is the one reading of the
// fleet's shape, and what it leaves out has no route through it either.
func fleetLinkAdjacency(fleet []rosterEntry, repositories []config.Repository) map[string][]string {
	isMember := make(map[string]bool, len(fleet))
	for _, entry := range fleet {
		isMember[globx.Fold(entry.name)] = isFleetRosterMember
	}
	composed := make(map[string]*config.Repository, len(repositories))
	for i := range repositories {
		composed[globx.Fold(repositories[i].Name)] = &repositories[i]
	}
	adjacency := make(map[string][]string, len(fleet))
	recorded := make(map[string]bool)
	for _, entry := range fleet {
		linker := globx.Fold(entry.name)
		// A fleet member this run never walked into declares nothing, and
		// LinkPeers answers nothing for it.
		for _, name := range composed[linker].LinkPeers() {
			peer := globx.Fold(name)
			if peer == linker || !isMember[peer] || recorded[linker+fleetLinkPairSeparator+peer] {
				continue
			}
			recorded[linker+fleetLinkPairSeparator+peer], recorded[peer+fleetLinkPairSeparator+linker] = isFleetLinkRecorded, isFleetLinkRecorded
			adjacency[linker] = append(adjacency[linker], peer)
			adjacency[peer] = append(adjacency[peer], linker)
		}
	}
	for _, peers := range adjacency {
		sort.Strings(peers)
	}
	return adjacency
}

// chooseLinkOwner answers which end of a proposed link writes it and which end
// it names. The link is created inside a checkout this run holds, so a
// composed end owns it; where both ends are composed the identity that sorts
// first under case folding owns it, so one fleet is computed the same way
// twice.
func chooseLinkOwner(first, second rosterEntry) (rosterEntry, rosterEntry) {
	if globx.Fold(second.name) < globx.Fold(first.name) {
		first, second = second, first
	}
	if first.composed {
		return first, second
	}
	return second, first
}
