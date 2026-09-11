package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const levelTrace = slog.Level(-8)

// command owns process-facing dependencies; the collection pipeline remains
// independent of command-line parsing, environment variables and exit codes.
type command struct {
	getenv func(string) string
	stderr io.Writer
	run    func(context.Context, Config, string) error
}

func main() {
	os.Exit(mainCode())
}

func mainCode() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return (command{getenv: os.Getenv, stderr: os.Stderr, run: Run}).execute(ctx, os.Args[1:])
}

func (c command) execute(root context.Context, args []string) int {
	flags := flag.NewFlagSet("download-counter", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	output := flags.String("output", "downloads.json", "snapshot output path")
	defaultLevel := "info"
	if c.getenv("DEBUG") == "1" {
		defaultLevel = "debug"
	}
	verbosity := flags.String("log-level", defaultLevel, "trace, debug, info, warn or error")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	level, valid := map[string]slog.Level{
		"trace": levelTrace, "debug": slog.LevelDebug, "info": slog.LevelInfo,
		"warn": slog.LevelWarn, "error": slog.LevelError,
	}[strings.ToLower(*verbosity)]
	if !valid || flags.NArg() != 0 || *output == "" {
		fmt.Fprintln(c.stderr, "invalid arguments: use -help for options")
		return 2
	}
	logger := slog.New(slog.NewJSONHandler(c.stderr, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, attribute slog.Attr) slog.Attr {
			if attribute.Key == slog.LevelKey && attribute.Value.Any() == levelTrace {
				return slog.String(slog.LevelKey, "TRACE")
			}
			return attribute
		},
	}))
	token := c.getenv("GITHUB_TOKEN")
	if strings.TrimSpace(token) == "" {
		logger.Error("GITHUB_TOKEN is required to collect GitHub release totals")
		return 1
	}
	ctx, cancel := context.WithTimeout(root, 2*time.Minute)
	defer cancel()
	client := &http.Client{
		Timeout:       20 * time.Second,
		Transport:     http.DefaultTransport.(*http.Transport).Clone(),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	err := c.run(ctx, Config{
		GitHubURL:   "https://api.github.com/graphql",
		DockerURL:   func(repository string) string { return "https://hub.docker.com/v2/repositories/" + repository + "/" },
		GitHubToken: token, Client: client, Now: time.Now, Logger: logger,
	}, *output)
	if err != nil {
		logger.ErrorContext(ctx, "snapshot refresh failed", "error", err)
		return 1
	}
	logger.InfoContext(ctx, "snapshot written", "path", *output)
	return 0
}
