package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/script"
	"github.com/yohimik/dispat/services/dispat/internal/workspaceenv"
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
	owners := ""
	explicitLive := false
	for _, pair := range env {
		if value, ok := strings.CutPrefix(pair, workspaceenv.Owners+"="); ok {
			owners = value
		}
		if value, ok := strings.CutPrefix(pair, workspaceenv.LivePins+"="); ok && value != "" {
			explicitLive = true
		}
	}
	// A nested selection can contain fewer packages than its parent run. Its
	// grandchildren must retain the original complete owner map that the live
	// context was bound to, rather than replacing it with the narrow plan.
	if !explicitLive && r.workspace.InheritedPinsEnabled() && os.Getenv(workspaceenv.LivePins) != "" {
		owners = os.Getenv(workspaceenv.Owners)
	}
	env = withoutEnvNames(env, workspaceRootEnv, workspaceConfigEnv, workspaceImportsEnv,
		workspaceenv.Owners, workspaceenv.Repositories)
	env = append(env, r.workspaceEnv...)
	env = append(env, workspaceenv.Owners+"="+owners)
	// Explicit global flags suppress inheritance even when they resolve to the
	// same paths. Clear the ambient coordinator unless this call carries the
	// parent release's explicit newly-created context.
	if !explicitLive && !r.workspace.InheritedPinsEnabled() && os.Getenv(workspaceenv.LivePins) != "" {
		env = append(env, workspaceenv.LivePins+"=")
	}
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
	var repositories []string
	for _, repo := range workspace.Repositories {
		if !repo.Control {
			repositories = append(repositories, repo.Name)
		}
		if !repo.Imported || repo.ConfigPath == "" {
			continue
		}
		path, err := filepath.Rel(workspace.ControlRoot, repo.ConfigPath)
		if err != nil {
			path = repo.ConfigPath
		}
		imports = append(imports, filepath.ToSlash(path))
	}
	sort.Strings(repositories)
	encoded, _ := json.Marshal(imports)
	encodedRepositories, _ := json.Marshal(repositories)
	return []string{
		workspaceRootEnv + "=" + workspace.ControlRoot,
		workspaceConfigEnv + "=" + filepath.ToSlash(configPath),
		workspaceImportsEnv + "=" + string(encoded),
		workspaceenv.Repositories + "=" + string(encodedRepositories),
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
