package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A successful HTTP response is not a complete measurement when GraphQL has
// omitted a requested connection field. In particular, nil nodes are different
// from the valid empty array returned for a release without assets.
func TestReviewIncompleteGraphQLCannotPublishZero(t *testing.T) {
	const valid = `{"data":{"repository":{"releases":{"nodes":[{"id":"r1","isDraft":false,"releaseAssets":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}`
	for _, field := range []string{"release nodes", "release hasNextPage", "draft", "asset nodes", "asset hasNextPage"} {
		for _, omitted := range []bool{false, true} {
			name := field + "/null"
			if omitted {
				name = field + "/omitted"
			}
			t.Run(name, func(t *testing.T) {
				var response map[string]any
				if err := json.Unmarshal([]byte(valid), &response); err != nil {
					t.Fatal(err)
				}
				releases := response["data"].(map[string]any)["repository"].(map[string]any)["releases"].(map[string]any)
				release := releases["nodes"].([]any)[0].(map[string]any)
				assets := release["releaseAssets"].(map[string]any)
				var object map[string]any
				var key string
				switch field {
				case "release nodes":
					object, key = releases, "nodes"
				case "release hasNextPage":
					object, key = releases["pageInfo"].(map[string]any), "hasNextPage"
				case "draft":
					object, key = release, "isDraft"
				case "asset nodes":
					object, key = assets, "nodes"
				case "asset hasNextPage":
					object, key = assets["pageInfo"].(map[string]any), "hasNextPage"
				}
				if omitted {
					delete(object, key)
				} else {
					object[key] = nil
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/docker/") {
						_, _ = io.WriteString(w, `{"pull_count":0}`)
						return
					}
					_ = json.NewEncoder(w).Encode(response)
				}))
				defer server.Close()
				if result, err := Collect(context.Background(), testConfig(server)); err == nil {
					t.Fatalf("accepted incomplete measurement: %+v", result)
				}
			})
		}
	}
}

func TestReviewCompletePipelineAndPartialProviderFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		github int64
		pulls  []int64
		status int
		want   string
	}{
		{"complete snapshot", 7, []int64{1, 2, 3, 4}, 200, ""},
		{"provider unavailable", 7, []int64{1, 2, 3, 4}, 503, "Docker Hub"},
		{"Docker aggregate overflow", 0, []int64{maxSafeInt, 1, 0, 0}, 200, "Docker Hub total"},
		{"combined overflow", maxSafeInt, []int64{1, 0, 0, 0}, 200, "combined total"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/graphql" {
					_, _ = fmt.Fprintf(w, `{"data":{"repository":{"releases":{"nodes":[{"id":"r","isDraft":false,"releaseAssets":{"nodes":[{"id":"a","downloadCount":%d}],"pageInfo":{"hasNextPage":false}}}],"pageInfo":{"hasNextPage":false}}}}}`, tc.github)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprintf(w, `{"pull_count":%d}`, tc.pulls[index])
				index++
			}))
			defer server.Close()
			output := filepath.Join(t.TempDir(), "downloads.json")
			if err := os.WriteFile(output, []byte("previous complete snapshot"), 0o644); err != nil {
				t.Fatal(err)
			}
			err := Run(context.Background(), testConfig(server), output)
			data, readErr := os.ReadFile(output)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) || string(data) != "previous complete snapshot" {
					t.Fatalf("partial result escaped: err=%v file=%s", err, data)
				}
				return
			}
			var snapshot Snapshot
			if err != nil || json.Unmarshal(data, &snapshot) != nil || snapshot.Total != 17 {
				t.Fatalf("invalid saved result: %s, %v", data, err)
			}
		})
	}
}

func TestReviewMalformedAndUnboundedAssetStreams(t *testing.T) {
	for _, scenario := range []string{"null release", "asset page bound", "truncated response"} {
		t.Run(scenario, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				switch scenario {
				case "null release":
					_, _ = io.WriteString(w, `{"data":{"repository":{"releases":{"nodes":[null],"pageInfo":{"hasNextPage":false}}}}}`)
				case "truncated response":
					w.Header().Set("Content-Length", "400")
					_, _ = io.WriteString(w, `{"data":`)
				default:
					if requests == 1 {
						_, _ = io.WriteString(w, `{"data":{"repository":{"releases":{"nodes":[{"id":"r","isDraft":false,"releaseAssets":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"start"}}}],"pageInfo":{"hasNextPage":false}}}}}`)
					} else {
						_, _ = fmt.Fprintf(w, `{"data":{"node":{"releaseAssets":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"page-%d"}}}}}`, requests)
					}
				}
			}))
			defer server.Close()
			if _, err := Collect(context.Background(), testConfig(server)); err == nil {
				t.Fatal("accepted incomplete or unbounded collection")
			}
			if requests > maxPages+1 {
				t.Fatalf("asset requests escaped bound: %d", requests)
			}
		})
	}
}
