package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProcessSetupCanShowHelpWithoutAuthentication(t *testing.T) {
	previousArgs := os.Args
	os.Args = []string{"download-counter", "-help"}
	t.Cleanup(func() { os.Args = previousArgs })
	t.Setenv("GITHUB_TOKEN", "")
	if code := mainCode(); code != 0 {
		t.Fatalf("help exited %d", code)
	}
}

func TestCommandBoundsAndConfiguresCollection(t *testing.T) {
	for _, verbosity := range []string{"trace", "debug", "info", "warn", "error"} {
		t.Run(verbosity, func(t *testing.T) {
			var logs bytes.Buffer
			var collectedContext context.Context
			c := command{
				getenv: func(key string) string {
					if key == "GITHUB_TOKEN" {
						return "private-test-token"
					}
					return ""
				},
				stderr: &logs,
				run: func(ctx context.Context, cfg Config, output string) error {
					collectedContext = ctx
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 2*time.Minute || time.Until(deadline) < time.Minute {
						t.Fatal("collection has no bounded deadline")
					}
					if output != "local.json" || cfg.GitHubToken != "private-test-token" || cfg.GitHubURL != "https://api.github.com/graphql" || cfg.DockerURL("owner/image") != "https://hub.docker.com/v2/repositories/owner/image/" {
						t.Fatal("wrong collection inputs")
					}
					if cfg.Client.Timeout != 20*time.Second || cfg.Client.CheckRedirect(nil, nil) != http.ErrUseLastResponse || cfg.Now().IsZero() {
						t.Fatal("request safety settings missing")
					}
					cfg.Logger.Log(ctx, levelTrace, "trace event")
					cfg.Logger.DebugContext(ctx, "debug event")
					cfg.Logger.WarnContext(ctx, "warning event")
					cfg.Logger.ErrorContext(ctx, "error event")
					return nil
				},
			}
			if code := c.execute(context.Background(), []string{"-output", "local.json", "-log-level", verbosity}); code != 0 {
				t.Fatalf("exit %d: %s", code, &logs)
			}
			if collectedContext.Err() != context.Canceled {
				t.Fatal("collection context retained after command finished")
			}
			if strings.Contains(logs.String(), "private-test-token") {
				t.Fatal("credential leaked to logs")
			}
			if strings.Contains(logs.String(), `"level":"TRACE"`) != (verbosity == "trace") {
				t.Fatalf("incorrect trace filtering: %s", &logs)
			}
		})
	}
}

func TestCommandRejectsInvalidInputsBeforeNetworkWork(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		token string
		code  int
	}{
		{"unknown flag", []string{"-unknown"}, "token", 2},
		{"help", []string{"-help"}, "", 0},
		{"unknown log level", []string{"-log-level", "verbose"}, "token", 2},
		{"positional argument", []string{"extra"}, "token", 2},
		{"empty output", []string{"-output", ""}, "token", 2},
		{"missing credential", nil, "", 1},
		{"blank credential", nil, "  ", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			c := command{stderr: &logs, getenv: func(string) string { return tc.token }, run: func(context.Context, Config, string) error {
				t.Fatal("invalid command attempted collection")
				return nil
			}}
			if code := c.execute(context.Background(), tc.args); code != tc.code {
				t.Fatalf("exit %d, want %d: %s", code, tc.code, &logs)
			}
		})
	}
}

func TestCommandReportsFailureAndHonorsDebugEnvironment(t *testing.T) {
	var logs bytes.Buffer
	root, cancel := context.WithCancel(context.Background())
	cancel()
	c := command{
		stderr: &logs,
		getenv: func(key string) string {
			if key == "DEBUG" {
				return "1"
			}
			return "token"
		},
		run: func(ctx context.Context, cfg Config, output string) error {
			if output != "downloads.json" || !cfg.Logger.Enabled(ctx, slog.LevelDebug) || !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("defaults or cancellation were lost")
			}
			return errors.New("source failed")
		},
	}
	if code := c.execute(root, nil); code != 1 || !strings.Contains(logs.String(), `"level":"ERROR"`) || !strings.Contains(logs.String(), "source failed") {
		t.Fatalf("missing error outcome: %d %s", code, &logs)
	}
}
