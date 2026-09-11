package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testConfig(server *httptest.Server) Config {
	return Config{GitHubURL: server.URL + "/graphql", DockerURL: func(repository string) string { return server.URL + "/docker/" + repository }, GitHubToken: "secret", Client: server.Client(), Now: func() time.Time { return time.Date(2026, 9, 11, 1, 2, 3, 0, time.FixedZone("x", 3600)) }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestCollectPaginatesDeduplicatesAndSums(t *testing.T) {
	var releaseCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/docker/") {
			if r.Header.Get("Authorization") != "" {
				t.Error("GitHub credential reached Docker Hub")
			}
			io.WriteString(w, `{"pull_count":10}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing GitHub credential")
		}
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(request.Query, "repository(owner") {
			releaseCalls++
			if request.Variables["releaseCursor"] == nil {
				io.WriteString(w, `{"data":{"repository":{"releases":{"nodes":[{"id":"r1","isDraft":false,"releaseAssets":{"nodes":[{"id":"a1","downloadCount":2}],"pageInfo":{"hasNextPage":true,"endCursor":"ac1"}}},{"id":"draft","isDraft":true,"releaseAssets":{"nodes":[{"id":"bad","downloadCount":999}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}],"pageInfo":{"hasNextPage":true,"endCursor":"rc1"}}}}}`)
			} else {
				io.WriteString(w, `{"data":{"repository":{"releases":{"nodes":[{"id":"r1","isDraft":false,"releaseAssets":{"nodes":[{"id":"a1","downloadCount":2}],"pageInfo":{"hasNextPage":false,"endCursor":""}}},{"id":"r2","isDraft":false,"releaseAssets":{"nodes":[{"id":"a3","downloadCount":5}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}`)
			}
			return
		}
		io.WriteString(w, `{"data":{"node":{"releaseAssets":{"nodes":[{"id":"a1","downloadCount":2},{"id":"a2","downloadCount":3}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}`)
	}))
	defer server.Close()
	snapshot, err := Collect(context.Background(), testConfig(server))
	if err != nil {
		t.Fatal(err)
	}
	if releaseCalls != 2 || snapshot.GitHub != 10 || snapshot.DockerHub != 40 || snapshot.Total != 50 {
		t.Fatalf("unexpected snapshot %#v, release calls %d", snapshot, releaseCalls)
	}
	if snapshot.CollectedAt != "2026-09-11T00:02:03Z" || len(snapshot.Repositories) != 4 {
		t.Fatalf("unexpected metadata %#v", snapshot)
	}
}

func TestCollectionFailuresPreserveOutput(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "downloads.json")
	if err := os.WriteFile(output, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, body string
		status     int
		want       string
	}{
		{"invalid JSON", `{`, 200, "decode response"},
		{"HTTP failure", `{}`, 429, "HTTP 429"},
		{"GraphQL failure", `{"errors":[{"message":"quota"}]}`, 200, "GraphQL error"},
		{"missing field", `{"data":{"repository":{"releases":{"nodes":[{"id":"r","isDraft":false,"releaseAssets":{"nodes":[{"id":"a"}],"pageInfo":{"hasNextPage":false}}}],"pageInfo":{"hasNextPage":false}}}}}`, 200, "missing or non-numeric"},
		{"string integer", `{"data":{"repository":{"releases":{"nodes":[{"id":"r","isDraft":false,"releaseAssets":{"nodes":[{"id":"a","downloadCount":"1"}],"pageInfo":{"hasNextPage":false}}}],"pageInfo":{"hasNextPage":false}}}}}`, 200, "non-numeric"},
		{"fraction", `{"data":{"repository":{"releases":{"nodes":[{"id":"r","isDraft":false,"releaseAssets":{"nodes":[{"id":"a","downloadCount":1.5}],"pageInfo":{"hasNextPage":false}}}],"pageInfo":{"hasNextPage":false}}}}}`, 200, "invalid non-negative"},
		{"negative", `{"data":{"repository":{"releases":{"nodes":[{"id":"r","isDraft":false,"releaseAssets":{"nodes":[{"id":"a","downloadCount":-1}],"pageInfo":{"hasNextPage":false}}}],"pageInfo":{"hasNextPage":false}}}}}`, 200, "invalid non-negative"},
		{"overflow", `{"data":{"repository":{"releases":{"nodes":[{"id":"r","isDraft":false,"releaseAssets":{"nodes":[{"id":"a","downloadCount":9007199254740992}],"pageInfo":{"hasNextPage":false}}}],"pageInfo":{"hasNextPage":false}}}}}`, 200, "invalid non-negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tt.status); io.WriteString(w, tt.body) }))
			defer server.Close()
			err := Run(context.Background(), testConfig(server), output)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
			if string(mustRead(t, output)) != "old\n" {
				t.Fatal("prior output changed")
			}
		})
	}
}

func TestResponseLimitCancellationAndRedirect(t *testing.T) {
	t.Run("limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat(" ", maxBodyBytes+1)) }))
		defer server.Close()
		_, err := Collect(context.Background(), testConfig(server))
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatal(err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
		defer server.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := Collect(ctx, testConfig(server))
		if err == nil {
			t.Fatal("expected cancellation")
		}
	})
	t.Run("redirect", func(t *testing.T) {
		targetToken := ""
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetToken = r.Header.Get("Authorization") }))
		defer target.Close()
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
		defer source.Close()
		cfg := testConfig(source)
		cfg.Client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		_, err := Collect(context.Background(), cfg)
		if err == nil || targetToken != "" {
			t.Fatalf("err=%v token=%q", err, targetToken)
		}
	})
}

func TestCursorLoopsAndAtomicWrite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"repository":{"releases":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"same"}}}}}`)
	}))
	defer server.Close()
	_, err := Collect(context.Background(), testConfig(server))
	if err == nil || !strings.Contains(err.Error(), "cursor") {
		t.Fatal(err)
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "downloads.json")
	snapshot := Snapshot{SchemaVersion: 1, CollectedAt: "2026-09-11T00:00:00Z"}
	if err := WriteAtomic(output, snapshot); err != nil {
		t.Fatal(err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(mustRead(t, output), &decoded); err != nil || decoded.SchemaVersion != 1 {
		t.Fatalf("%#v %v", decoded, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary file remained: %v", entries)
	}
}

func TestIntegerAndAdd(t *testing.T) {
	for _, value := range []json.Number{"", "-1", "1.2", "x", "9007199254740992"} {
		if _, err := integer(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	if got, err := add(maxSafeInt, 0); err != nil || got != maxSafeInt {
		t.Fatal(got, err)
	}
	if _, err := add(maxSafeInt, 1); err == nil {
		t.Fatal("accepted overflow")
	}
}

func TestAssetValidationAndPaginationFailures(t *testing.T) {
	base := func() collector {
		return collector{cfg: Config{Client: http.DefaultClient, GitHubURL: "http://[::1", Logger: slog.Default()}, seenAssets: map[string]bool{}, seenReleases: map[string]bool{}}
	}
	for _, tc := range []struct {
		name, json, want string
		total            int64
	}{
		{"missing connection", `{}`, "incomplete", 0},
		{"null asset", `{"nodes":[null],"pageInfo":{"hasNextPage":false}}`, "null asset", 0},
		{"missing id", `{"nodes":[{"downloadCount":1}],"pageInfo":{"hasNextPage":false}}`, "without id", 0},
		{"sum overflow", `{"nodes":[{"id":"a","downloadCount":1}],"pageInfo":{"hasNextPage":false}}`, "overflow", maxSafeInt},
		{"missing cursor", `{"nodes":[],"pageInfo":{"hasNextPage":true}}`, "cursor", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			_, err := c.assets(context.Background(), tc.total, "r", decodeAssets(t, tc.json))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v", err)
			}
		})
	}
	t.Run("GraphQL request failure", func(t *testing.T) {
		c := base()
		_, err := c.assets(context.Background(), 0, "r", decodeAssets(t, `{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}`))
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("missing paginated node", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"data":{}}`) }))
		defer s.Close()
		c := base()
		c.cfg.GitHubURL = s.URL
		c.cfg.Client = s.Client()
		_, err := c.assets(context.Background(), 0, "r", decodeAssets(t, `{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}`))
		if err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatal(err)
		}
	})
	t.Run("cursor loop", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"data":{"node":{"releaseAssets":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"same"}}}}}`)
		}))
		defer s.Close()
		c := base()
		c.cfg.GitHubURL = s.URL
		c.cfg.Client = s.Client()
		_, err := c.assets(context.Background(), 0, "r", decodeAssets(t, `{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"same"}}`))
		if err == nil || !strings.Contains(err.Error(), "cursor") {
			t.Fatal(err)
		}
	})
}

func TestReleaseAndRequestBounds(t *testing.T) {
	t.Run("release pages", func(t *testing.T) {
		page := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page++
			io.WriteString(w, `{"data":{"repository":{"releases":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"`+strconv.Itoa(page)+`"}}}}}`)
		}))
		defer s.Close()
		cfg := testConfig(s)
		c := collector{cfg: cfg, seenAssets: map[string]bool{}, seenReleases: map[string]bool{}}
		_, err := c.github(context.Background())
		if err == nil || !strings.Contains(err.Error(), "page limit") {
			t.Fatal(err)
		}
	})
	t.Run("request budget", func(t *testing.T) {
		c := collector{cfg: Config{Client: http.DefaultClient}}
		c.requests = maxRequests
		req, _ := http.NewRequest(http.MethodGet, "http://localhost", nil)
		if err := c.request(req, &struct{}{}); err == nil || !strings.Contains(err.Error(), "request limit") {
			t.Fatal(err)
		}
	})
	t.Run("trailing JSON", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{} {}`) }))
		defer s.Close()
		c := collector{cfg: Config{Client: s.Client()}}
		req, _ := http.NewRequest(http.MethodGet, s.URL, nil)
		if err := c.request(req, &struct{}{}); err == nil || !strings.Contains(err.Error(), "trailing") {
			t.Fatal(err)
		}
	})
}

func TestDockerValidationAndConfiguration(t *testing.T) {
	if _, err := Collect(context.Background(), Config{}); err == nil {
		t.Fatal("accepted incomplete config")
	}
	for _, body := range []string{`{}`, `{"pull_count":-1}`, `{"pull_count":1.5}`, `{"pull_count":"1"}`, `{"pull_count":9007199254740992}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		cfg := testConfig(s)
		c := collector{cfg: cfg}
		if _, err := c.docker(context.Background(), "repo"); err == nil {
			t.Errorf("accepted %s", body)
		}
		s.Close()
	}
	cfg := Config{DockerURL: func(string) string { return "://" }, Client: http.DefaultClient}
	c := collector{cfg: cfg}
	if _, err := c.docker(context.Background(), "repo"); err == nil {
		t.Fatal("accepted bad URL")
	}
}

func TestAtomicWriteAndRunErrors(t *testing.T) {
	if err := WriteAtomic(filepath.Join(t.TempDir(), "missing", "out.json"), Snapshot{}); err == nil {
		t.Fatal("expected create error")
	}
	if err := Run(context.Background(), Config{}, filepath.Join(t.TempDir(), "out.json")); err == nil {
		t.Fatal("expected config error")
	}
}

func decodeAssets(t *testing.T, value string) assetConnection {
	t.Helper()
	var assets assetConnection
	if err := json.Unmarshal([]byte(value), &assets); err != nil {
		t.Fatal(err)
	}
	return assets
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
