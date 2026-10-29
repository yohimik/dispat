package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSSHConnectionPolicyPreservesOneCommandAndStreams(t *testing.T) {
	remoteFailure := errors.New("exit status 255")
	for _, scenario := range []struct {
		name      string
		failure   error
		isStopped bool
	}{
		{name: "success"},
		{name: "remote exit 255", failure: remoteFailure},
		{name: "caller cancellation", isStopped: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			deadline := time.Now().Add(time.Minute)
			ctx, cancel := context.WithDeadline(t.Context(), deadline)
			defer cancel()
			d := &driver{settings: options{Root: t.TempDir()}}
			var worker instance
			if err := json.Unmarshal([]byte(`{"name":"worker-1","networkInterfaces":[{"accessConfigs":[{"natIP":"127.0.0.1"}]}]}`), &worker); err != nil {
				t.Fatal(err)
			}
			input := strings.NewReader("unchanged-input")
			var output bytes.Buffer
			calls := 0
			d.runCommand = func(received context.Context, request invocation) ([]byte, error) {
				calls++
				actualDeadline, hasDeadline := received.Deadline()
				if received != ctx || !hasDeadline || !actualDeadline.Equal(deadline) {
					t.Fatal("SSH replaced the caller context or deadline")
				}
				wantArgs := []string{
					"-i", filepath.Join(d.settings.Root, "private/key"),
					"-o", "IdentitiesOnly=yes",
					"-o", "UserKnownHostsFile=" + filepath.Join(d.settings.Root, "private/known_hosts"),
					"-o", "StrictHostKeyChecking=yes",
					"-o", "ForwardAgent=no",
					"-o", "ConnectTimeout=15",
					"-o", "ConnectionAttempts=3",
					"-o", "BatchMode=yes",
					"dispat@127.0.0.1",
					`'printf' '%s' 'literal '"'"'quoted'"'"' value'`,
				}
				if request.Name != "ssh" || !slices.Equal(request.Args, wantArgs) {
					t.Fatalf("SSH connection policy or command changed: %+v", request)
				}
				if request.Input != input || request.Output != &output {
					t.Fatal("SSH replaced the command streams")
				}
				prefix := make([]byte, 2)
				if _, err := io.ReadFull(request.Input, prefix); err != nil || string(prefix) != "un" {
					t.Fatalf("command input replayed or changed: %q, %v", prefix, err)
				}
				if _, err := io.WriteString(request.Output, "streamed output"); err != nil {
					t.Fatal(err)
				}
				if scenario.isStopped {
					cancel()
					return []byte("captured output"), received.Err()
				}
				return []byte("captured output"), scenario.failure
			}
			body, err := d.ssh(ctx, worker, invocation{
				Name: "printf", Args: []string{"%s", "literal 'quoted' value"}, Input: input, Output: &output,
			})
			wantError := scenario.failure
			if scenario.isStopped {
				wantError = context.Canceled
			}
			if !errors.Is(err, wantError) || calls != 1 {
				t.Fatalf("SSH result %v after %d invocations, expected %v after exactly one", err, calls, wantError)
			}
			remaining, readErr := io.ReadAll(input)
			if readErr != nil || string(remaining) != "changed-input" || string(body) != "captured output" || output.String() != "streamed output" {
				t.Fatalf("SSH altered input/output progress: remaining %q, body %q, stream %q, error %v", remaining, body, output.String(), readErr)
			}
		})
	}
}
