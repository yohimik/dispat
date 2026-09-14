package app

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

const (
	workspaceRootEnv    = "DISPAT_INTERNAL_WORKSPACE_ROOT"
	workspaceConfigEnv  = "DISPAT_INTERNAL_WORKSPACE_CONFIG"
	workspaceImportsEnv = "DISPAT_INTERNAL_WORKSPACE_CONFIGS"
)

// packageRunner dispatches every package or space script through the shell of
// the config that owns its working directory. It also carries the composed
// workspace into nested dispat commands, so a source-local script can invoke
// a fleet command without rediscovering only its own config.
func (a *App) packageRunner() script.Runner {
	if a.workspace == nil {
		return &script.ShellRunner{Shell: a.cfg.Shell, Log: a.log}
	}
	return &workspaceScriptRunner{workspace: a.workspace, fallback: a.cfg.Shell,
		log: a.log, workspaceEnv: workspaceContextEnv(a.workspace)}
}

type workspaceScriptRunner struct {
	workspace    *config.Workspace
	fallback     []string
	log          zerolog.Logger
	workspaceEnv []string
}

func (r *workspaceScriptRunner) Run(ctx context.Context, dir, command string, env []string, stdout, stderr io.Writer) error {
	shell := r.fallback
	if repo := r.workspace.RepositoryForDir(dir); repo != nil && repo.Config != nil {
		shell = repo.Config.Shell
	}
	env = withoutEnvNames(env, workspaceRootEnv, workspaceConfigEnv, workspaceImportsEnv)
	env = append(env, r.workspaceEnv...)
	return (&script.ShellRunner{Shell: shell, Log: r.log}).Run(ctx, dir, command, env, stdout, stderr)
}

func workspaceContextEnv(workspace *config.Workspace) []string {
	control := workspace.RepositoryByName(config.ControlRepository)
	if control == nil {
		return nil
	}
	configPath, err := filepath.Rel(workspace.ControlRoot, control.ConfigPath)
	if err != nil {
		configPath = control.ConfigPath
	}
	var imports []string
	for _, repo := range workspace.Repositories {
		if !repo.Imported || repo.ConfigPath == "" {
			continue
		}
		path, err := filepath.Rel(workspace.ControlRoot, repo.ConfigPath)
		if err != nil {
			path = repo.ConfigPath
		}
		imports = append(imports, filepath.ToSlash(path))
	}
	encoded, _ := json.Marshal(imports)
	return []string{
		workspaceRootEnv + "=" + workspace.ControlRoot,
		workspaceConfigEnv + "=" + filepath.ToSlash(configPath),
		workspaceImportsEnv + "=" + string(encoded),
	}
}

func withoutEnvNames(env []string, names ...string) []string {
	drop := make(map[string]bool, len(names))
	for _, name := range names {
		drop[name] = true
	}
	out := env[:0:0]
	for _, pair := range env {
		name, _, _ := strings.Cut(pair, "=")
		if !drop[name] {
			out = append(out, pair)
		}
	}
	return out
}
