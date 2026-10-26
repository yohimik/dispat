package app

import (
	"context"
	"net/url"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// planningRemoteCoordinate is the credential-free semantic portion of a Git
// remote URL. The source URL itself may contain credentials and never enters
// a signed planning document.
type planningRemoteCoordinate struct {
	host  string
	owner string
	repo  string
}

func (c planningRemoteCoordinate) url() string {
	if c.host == pinnedPlanningAbsentText || c.owner == pinnedPlanningAbsentText || c.repo == pinnedPlanningAbsentText {
		return pinnedPlanningAbsentText
	}
	return (&url.URL{Scheme: pinnedPlanningRemoteSchemeName, Host: c.host, Path: pinnedPlanningRemotePathSeparatorText + c.owner + pinnedPlanningRemotePathSeparatorText + c.repo}).String()
}

func parsePlanningRemote(raw string) planningRemoteCoordinate {
	var host, path string
	if strings.Contains(raw, pinnedPlanningURLSchemeSeparatorText) {
		parsed, err := url.Parse(raw)
		if err != nil {
			return planningRemoteCoordinate{}
		}
		host, path = parsed.Hostname(), parsed.Path
	} else if colon := strings.IndexByte(raw, pinnedPlanningRemoteHostDelimiterByte); colon > pinnedPlanningTextStartOffsetBytes {
		host, path = raw[:colon], raw[colon+pinnedPlanningDelimiterBytes:]
		if at := strings.LastIndexByte(host, pinnedPlanningRemoteUserDelimiterByte); at >= pinnedPlanningTextStartOffsetBytes {
			host = host[at+pinnedPlanningDelimiterBytes:]
		}
	}
	parts := strings.Split(strings.TrimSuffix(strings.Trim(path, pinnedPlanningRemotePathSeparatorText), pinnedPlanningGitSuffixText), pinnedPlanningRemotePathSeparatorText)
	if host == pinnedPlanningAbsentText || len(parts) != pinnedPlanningRemotePartCount || parts[pinnedPlanningRemoteOwnerIndex] == pinnedPlanningAbsentText || parts[pinnedPlanningRemoteRepositoryIndex] == pinnedPlanningAbsentText {
		return planningRemoteCoordinate{}
	}
	return planningRemoteCoordinate{host: host, owner: parts[pinnedPlanningRemoteOwnerIndex], repo: parts[pinnedPlanningRemoteRepositoryIndex]}
}

func (a *App) capturePlanningRemotes(ctx context.Context) (map[string]planningRemoteCoordinate, error) {
	if a.workspace == nil {
		return nil, nil
	}
	identities := make(map[string]planningRemoteCoordinate, len(a.workspace.Repositories))
	for _, repository := range a.workspace.Repositories {
		if repository.Control {
			continue
		}
		remote := pinnedPlanningDefaultRemoteName
		if repository.Commit != nil && repository.Commit.Remote != pinnedPlanningAbsentText {
			remote = repository.Commit.Remote
		}
		git := a.openRepositoryGit(repository.Name)
		raw, err := git.RemoteURL(ctx, remote)
		if err != nil {
			// The existing planner resolves an unavailable destination to no
			// coordinates. That absence is pinned too, rather than retried
			// against a later ambient remote configuration.
			identities[repository.Name] = planningRemoteCoordinate{}
			continue
		}
		identities[repository.Name] = parsePlanningRemote(raw)
	}
	return identities, nil
}

func (a *App) pinnedRepositoryCoordinates() []execution.PinnedRepositoryCoordinate {
	out := make([]execution.PinnedRepositoryCoordinate, pinnedPlanningInitialEntryCount, len(a.planningRemotes))
	for repository, identity := range a.planningRemotes {
		out = append(out, execution.PinnedRepositoryCoordinate{
			Repository: repository, Host: identity.host, Owner: identity.owner, Repo: identity.repo,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repository < out[j].Repository })
	return out
}
