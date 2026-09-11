package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	maxBodyBytes = 8 << 20
	maxPages     = 100
	maxRequests  = 500
	maxSafeInt   = int64(1<<53 - 1)
)

var dockerRepositories = []string{"yohimik/dispat-alpine", "yohimik/dispat-debian", "yohimik/dispat-ubuntu", "yohimik/dispat-dind"}

type Config struct {
	GitHubURL   string
	DockerURL   func(string) string
	GitHubToken string
	Client      *http.Client
	Now         func() time.Time
	Logger      *slog.Logger
}

type RepositoryTotal struct {
	Repository string `json:"repository"`
	Pulls      int64  `json:"pulls"`
}

type Snapshot struct {
	SchemaVersion int               `json:"schemaVersion"`
	Total         int64             `json:"total"`
	GitHub        int64             `json:"github"`
	DockerHub     int64             `json:"dockerHub"`
	CollectedAt   string            `json:"collectedAt"`
	Repositories  []RepositoryTotal `json:"repositories"`
}

type graphResponse struct {
	Data struct {
		Repository *struct {
			Releases *connection `json:"releases"`
		} `json:"repository"`
		Node *struct {
			Assets *assetConnection `json:"releaseAssets"`
		} `json:"node"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type connection struct {
	Nodes *[]*struct {
		ID      string           `json:"id"`
		IsDraft *bool            `json:"isDraft"`
		Assets  *assetConnection `json:"releaseAssets"`
	} `json:"nodes"`
	PageInfo *pageInfo `json:"pageInfo"`
}

type assetConnection struct {
	Nodes *[]*struct {
		ID            string          `json:"id"`
		DownloadCount json.RawMessage `json:"downloadCount"`
	} `json:"nodes"`
	PageInfo *pageInfo `json:"pageInfo"`
}

type pageInfo struct {
	HasNextPage *bool  `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

func Collect(ctx context.Context, cfg Config) (Snapshot, error) {
	if cfg.Client == nil || cfg.Now == nil || cfg.Logger == nil || cfg.GitHubURL == "" || cfg.DockerURL == nil {
		return Snapshot{}, errors.New("incomplete collector configuration")
	}
	c := collector{cfg: cfg, seenReleases: map[string]bool{}, seenAssets: map[string]bool{}}
	cfg.Logger.InfoContext(ctx, "collecting distribution totals")
	github, err := c.github(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("GitHub: %w", err)
	}
	repositories := make([]RepositoryTotal, 0, len(dockerRepositories))
	var docker int64
	for _, repository := range dockerRepositories {
		pulls, err := c.docker(ctx, repository)
		if err != nil {
			return Snapshot{}, fmt.Errorf("Docker Hub %s: %w", repository, err)
		}
		docker, err = add(docker, pulls)
		if err != nil {
			return Snapshot{}, fmt.Errorf("Docker Hub total: %w", err)
		}
		repositories = append(repositories, RepositoryTotal{repository, pulls})
	}
	total, err := add(github, docker)
	if err != nil {
		return Snapshot{}, fmt.Errorf("combined total: %w", err)
	}
	s := Snapshot{1, total, github, docker, cfg.Now().UTC().Format(time.RFC3339), repositories}
	cfg.Logger.InfoContext(ctx, "collected distribution totals", "total", total, "github", github, "dockerHub", docker)
	return s, nil
}

type collector struct {
	cfg          Config
	requests     int
	seenReleases map[string]bool
	seenAssets   map[string]bool
}

const releasesQuery = `query($releaseCursor:String){repository(owner:"yohimik",name:"dispat"){releases(first:100,after:$releaseCursor,orderBy:{field:CREATED_AT,direction:DESC}){nodes{id isDraft releaseAssets(first:100){nodes{id downloadCount}pageInfo{hasNextPage endCursor}}}pageInfo{hasNextPage endCursor}}}}`
const assetsQuery = `query($releaseID:ID!,$assetCursor:String){node(id:$releaseID){... on Release{releaseAssets(first:100,after:$assetCursor){nodes{id downloadCount}pageInfo{hasNextPage endCursor}}}}}`

func (c *collector) github(ctx context.Context) (int64, error) {
	var total int64
	var cursor any
	seenCursors := map[string]bool{}
	for page := 0; ; page++ {
		if page >= maxPages {
			return 0, errors.New("release page limit exceeded")
		}
		var response graphResponse
		if err := c.graph(ctx, releasesQuery, map[string]any{"releaseCursor": cursor}, &response); err != nil {
			return 0, err
		}
		if response.Data.Repository == nil || response.Data.Repository.Releases == nil || response.Data.Repository.Releases.PageInfo == nil || response.Data.Repository.Releases.Nodes == nil || response.Data.Repository.Releases.PageInfo.HasNextPage == nil {
			return 0, errors.New("repository releases missing from response")
		}
		for _, release := range *response.Data.Repository.Releases.Nodes {
			if release == nil {
				return 0, errors.New("null release in response")
			}
			if release.ID == "" || release.IsDraft == nil {
				return 0, errors.New("release id or draft state missing")
			}
			if *release.IsDraft || c.seenReleases[release.ID] {
				continue
			}
			if release.Assets == nil || release.Assets.PageInfo == nil || release.Assets.Nodes == nil || release.Assets.PageInfo.HasNextPage == nil {
				return 0, fmt.Errorf("release %s assets missing from response", release.ID)
			}
			c.seenReleases[release.ID] = true
			var err error
			total, err = c.assets(ctx, total, release.ID, *release.Assets)
			if err != nil {
				return 0, err
			}
		}
		info := *response.Data.Repository.Releases.PageInfo
		c.cfg.Logger.DebugContext(ctx, "collected GitHub release page", "page", page+1, "releases", len(*response.Data.Repository.Releases.Nodes))
		if !*info.HasNextPage {
			break
		}
		if info.EndCursor == "" || seenCursors[info.EndCursor] {
			return 0, errors.New("invalid release pagination cursor")
		}
		seenCursors[info.EndCursor] = true
		cursor = info.EndCursor
	}
	return total, nil
}

func (c *collector) assets(ctx context.Context, total int64, releaseID string, assets assetConnection) (int64, error) {
	seenCursors := map[string]bool{}
	for page := 0; ; page++ {
		if page >= maxPages {
			return 0, fmt.Errorf("release %s asset page limit exceeded", releaseID)
		}
		if assets.Nodes == nil || assets.PageInfo == nil || assets.PageInfo.HasNextPage == nil {
			return 0, fmt.Errorf("release %s asset connection incomplete", releaseID)
		}
		for _, asset := range *assets.Nodes {
			if asset == nil {
				return 0, fmt.Errorf("release %s has null asset", releaseID)
			}
			if asset.ID == "" {
				return 0, fmt.Errorf("release %s has asset without id", releaseID)
			}
			if c.seenAssets[asset.ID] {
				continue
			}
			count, err := integerRaw(asset.DownloadCount)
			if err != nil {
				return 0, fmt.Errorf("asset %s: %w", asset.ID, err)
			}
			total, err = add(total, count)
			if err != nil {
				return 0, fmt.Errorf("GitHub total: %w", err)
			}
			c.seenAssets[asset.ID] = true
		}
		if !*assets.PageInfo.HasNextPage {
			return total, nil
		}
		cursor := assets.PageInfo.EndCursor
		if cursor == "" || seenCursors[cursor] {
			return 0, fmt.Errorf("release %s has invalid asset pagination cursor", releaseID)
		}
		seenCursors[cursor] = true
		var response graphResponse
		if err := c.graph(ctx, assetsQuery, map[string]any{"releaseID": releaseID, "assetCursor": cursor}, &response); err != nil {
			return 0, err
		}
		if response.Data.Node == nil || response.Data.Node.Assets == nil || response.Data.Node.Assets.Nodes == nil || response.Data.Node.Assets.PageInfo == nil || response.Data.Node.Assets.PageInfo.HasNextPage == nil {
			return 0, fmt.Errorf("release %s missing from response", releaseID)
		}
		assets = *response.Data.Node.Assets
	}
}

func (c *collector) graph(ctx context.Context, query string, variables map[string]any, output *graphResponse) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.GitHubURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.cfg.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.GitHubToken)
	}
	if err := c.request(req, output); err != nil {
		return err
	}
	if len(output.Errors) > 0 {
		return fmt.Errorf("GraphQL error: %s", output.Errors[0].Message)
	}
	return nil
}

func (c *collector) docker(ctx context.Context, repository string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.DockerURL(repository), nil)
	if err != nil {
		return 0, err
	}
	var response struct {
		PullCount json.RawMessage `json:"pull_count"`
	}
	if err := c.request(req, &response); err != nil {
		return 0, err
	}
	pulls, err := integerRaw(response.PullCount)
	if err != nil {
		return 0, fmt.Errorf("pull_count: %w", err)
	}
	c.cfg.Logger.DebugContext(ctx, "collected Docker Hub repository", "repository", repository, "pulls", pulls)
	return pulls, nil
}

func (c *collector) request(req *http.Request, output any) error {
	c.requests++
	if c.requests > maxRequests {
		return errors.New("request limit exceeded")
	}
	if c.cfg.Logger != nil {
		c.cfg.Logger.Log(req.Context(), levelTrace, "requesting upstream", "host", req.URL.Host, "path", req.URL.Path, "request", c.requests)
	}
	res, err := c.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBodyBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxBodyBytes {
		return fmt.Errorf("response exceeds %d bytes", maxBodyBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("response has trailing data")
	}
	return nil
}

func integer(value json.Number) (int64, error) {
	if value == "" {
		return 0, errors.New("missing integer")
	}
	n, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil || n < 0 || n > maxSafeInt {
		return 0, fmt.Errorf("invalid non-negative safe integer %q", value)
	}
	return n, nil
}

func integerRaw(value json.RawMessage) (int64, error) {
	if len(value) == 0 || value[0] == '"' {
		return 0, errors.New("missing or non-numeric integer")
	}
	return integer(json.Number(value))
}

func add(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > maxSafeInt-b || b > maxSafeInt {
		return 0, errors.New("safe integer overflow")
	}
	return a + b, nil
}

func WriteAtomic(path string, snapshot Snapshot) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".downloads-*.json")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o644); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func Run(ctx context.Context, cfg Config, path string) error {
	snapshot, err := Collect(ctx, cfg)
	if err != nil {
		if cfg.Logger != nil {
			cfg.Logger.WarnContext(ctx, "keeping previous snapshot after collection failure", "error", err)
		}
		return err
	}
	return WriteAtomic(path, snapshot)
}
