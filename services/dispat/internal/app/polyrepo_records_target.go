package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// resolveRepositoryRecords resolves source coordinates once per repository.
// Explicit targets survive; an unresolved source never borrows control CI
// identity for either publication or generated links.
func (a *App) resolveRepositoryRecords(ctx context.Context, pkgs []*model.Package) error {
	remotes := make(map[string]string)
	for _, p := range pkgs {
		r := a.workspace.RepositoryForPackage(p)
		if r == nil {
			return fmt.Errorf("package %s has no repository owner", p.Name)
		}
		if r.Control {
			continue
		}
		raw, seen := remotes[r.Name]
		if !seen {
			remote := "origin"
			if r.Commit != nil && r.Commit.Remote != "" {
				remote = r.Commit.Remote
			}
			g := &gitx.CLI{Dir: r.Root, Log: a.log}
			raw, _ = g.RemoteURL(ctx, remote)
			remotes[r.Name] = raw
		}
		owner, repo := githubCoordinates(raw, p.GitHub.APIURL)
		a.log.Debug().Str("repository", r.Name).Str("owner", owner).Str("repo", repo).Msg("resolved source record destination")
		if p.GitHub.Owner == "" && p.GitHub.Repo == "" {
			p.GitHub.Owner, p.GitHub.Repo = owner, repo
		}
		p.GitHub.Format.RepositoryResolved = true
		if p.GitHub.Format.LinkOwner == "" && p.GitHub.Format.LinkRepo == "" {
			p.GitHub.Format.LinkOwner, p.GitHub.Format.LinkRepo = p.GitHub.Owner, p.GitHub.Repo
		}
		p.Changelog.Format.RepositoryResolved = true
		if p.Changelog.Format.LinkOwner == "" && p.Changelog.Format.LinkRepo == "" {
			p.Changelog.Format.LinkOwner, p.Changelog.Format.LinkRepo = p.GitHub.Owner, p.GitHub.Repo
		}
	}
	return nil
}

func githubCoordinates(raw, apiURL string) (string, string) {
	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", ""
		}
		host, path = u.Hostname(), u.Path
	} else if colon := strings.IndexByte(raw, ':'); colon > 0 {
		host, path = raw[:colon], raw[colon+1:]
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
	} else {
		return "", ""
	}
	want := "github.com"
	if apiURL != "" {
		u, err := url.Parse(apiURL)
		if err != nil {
			return "", ""
		}
		if u.Hostname() != "api.github.com" {
			want = u.Hostname()
		}
	}
	if want == "" || !strings.EqualFold(host, want) {
		return "", ""
	}
	parts := strings.Split(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}
	return parts[0], parts[1]
}
