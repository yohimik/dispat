package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBareRemotePushDoesNotStartAutomaticMaintenance(t *testing.T) {
	repository := &Repo{T: t, Root: t.TempDir()}
	repository.Git("init", "-q")
	repository.Git("config", "user.email", "integration@dispat.test")
	repository.Git("config", "user.name", "dispat integration")
	remote := repository.AddBareRemote()
	// The positive control must remain attached even when it repacks objects.
	repository.Git("-C", remote, "config", "gc.autoDetach", "false")
	repository.Git("-C", remote, "config", "maintenance.autoDetach", "false")
	repository.Git("-C", remote, "config", "gc.auto", "1")
	for _, control := range []struct {
		name     string
		isGCAuto bool
	}{
		{name: "disposable remote"},
		{name: "explicit automatic GC", isGCAuto: true},
	} {
		t.Run(control.name, func(t *testing.T) {
			if control.isGCAuto {
				repository.Git("-C", remote, "config", "receive.autogc", "true")
			}
			repository.CommitEmpty("test: exercise remote push maintenance")
			trace := filepath.Join(t.TempDir(), "git-trace.json")
			t.Setenv("GIT_TRACE2_EVENT", trace)
			repository.Git("push", "-q", "origin", "HEAD:refs/heads/"+DefaultBranch)
			contents, err := os.ReadFile(trace)
			require.NoError(t, err)
			var automaticMaintenance int
			var receivePack int
			for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
				var event struct {
					Event string   `json:"event"`
					Argv  []string `json:"argv"`
				}
				require.NoError(t, json.Unmarshal([]byte(line), &event))
				if event.Event != "child_start" {
					continue
				}
				for _, argument := range event.Argv {
					if strings.Contains(argument, "git-receive-pack") {
						receivePack++
					}
				}
				if len(event.Argv) >= 3 && filepath.Base(event.Argv[0]) == "git" &&
					(event.Argv[1] == "gc" || event.Argv[1] == "maintenance") {
					for _, argument := range event.Argv {
						if argument == "--auto" {
							automaticMaintenance++
						}
					}
				}
			}
			require.Positive(t, receivePack, "the trace must include the receiving Git process")
			if control.isGCAuto {
				require.Positive(t, automaticMaintenance, "the control must exercise receive-side automatic maintenance")
				return
			}
			require.Zero(t, automaticMaintenance, "a disposable remote must not start maintenance after a push")
		})
	}
}
