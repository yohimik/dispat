package app

import (
	"context"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/script"
	"github.com/yohimik/dispat/services/dispat/internal/workspaceenv"
)

// workspacePins carries verified native source commits into later scripts,
// including independent packages whose outputs are not dependency inputs.
// It has its own mutex: record hooks run while their repository is locked.
type workspacePins struct {
	mu     sync.Mutex
	values map[string]string
	root   string
	config string
	live   *workspaceenv.LivePinStore
	log    zerolog.Logger
	// inherited is true only when CLI composition accepted the inherited live
	// context. Matching explicit --root/--config flags do not grant access.
	inherited bool
}

func newWorkspacePins(a *App) *workspacePins {
	pins := &workspacePins{}
	if a != nil && a.workspace != nil {
		pins.log = a.log
		pins.inherited = a.workspace.InheritedPinsEnabled()
		pins.root = a.workspace.ControlRoot
		if control := a.workspace.RepositoryByName(config.ControlRepository); control != nil {
			pins.config = control.ConfigPath
		}
	}
	return pins
}

func workspacePinRepositories(workspace *config.Workspace) []string {
	if workspace == nil {
		return nil
	}
	repositories := make([]string, 0, len(workspace.Repositories))
	for _, repository := range workspace.Repositories {
		if !repository.Control && repository.Name != "" {
			repositories = append(repositories, repository.Name)
		}
	}
	sort.Strings(repositories)
	return repositories
}

func workspacePinOwners(pl *plan.Plan) map[string]string {
	owners := make(map[string]string)
	conflicts := make(map[string]bool)
	if pl == nil {
		return owners
	}
	for name, rel := range pl.Releases {
		if rel == nil || rel.Pkg == nil || rel.Pkg.Repository == "" {
			continue
		}
		key := plan.PackageCommitExportPrefix + plan.EnvKey(name)
		if previous, exists := owners[key]; exists && previous != rel.Pkg.Repository {
			conflicts[key] = true
		}
		owners[key] = rel.Pkg.Repository
	}
	for key := range conflicts {
		delete(owners, key)
	}
	return owners
}

func (p *workspacePins) start(root, configPath string, owners map[string]string, repositories []string) (func(), error) {
	live, err := workspaceenv.NewLivePins(root, configPath, owners, repositories)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.root, p.config, p.live = root, configPath, live
	p.mu.Unlock()
	p.log.Debug().Int("repositories", len(repositories)).Msg("created live workspace pin context")
	var once sync.Once
	return func() {
		once.Do(func() {
			if err := live.Close(); err != nil {
				p.log.Warn().Err(err).Msg("unable to remove live workspace pin context")
			}
		})
	}, nil
}

func (p *workspacePins) remember(owner string, rel *plan.Release, pin string) error {
	if p == nil || owner == config.ControlRepository || rel == nil || rel.Pkg == nil || rel.Pkg.Repository != owner || !fullObjectID(pin) {
		return nil
	}
	p.mu.Lock()
	live, root, configPath := p.live, p.root, p.config
	p.mu.Unlock()
	var err error
	if live != nil {
		err = live.Remember(owner, pin)
	} else if p.inherited && root != "" && configPath != "" {
		err = workspaceenv.RememberLivePin(root, configPath, os.Environ(), owner, pin)
	}
	if err != nil {
		return err
	}
	p.log.Trace().Str("repository", owner).Str("revision", pin).Msg("remembered live workspace pin")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.values == nil {
		p.values = make(map[string]string)
	}
	p.values["DISPAT_OUTPUT_"+plan.PackageCommitExportPrefix+plan.EnvKey(rel.Pkg.Name)] = pin
	return nil
}

func (p *workspacePins) environment(env []string) []string {
	p.mu.Lock()
	pairs := make([]string, 0, len(p.values)+len(env))
	for key, pin := range p.values {
		pairs = append(pairs, key+"="+pin)
	}
	live := p.live
	p.mu.Unlock()
	sort.Strings(pairs)
	// The active sequence's explicit exports take precedence over an earlier
	// native record of the same package. Never mutate the caller's environment.
	pairs = append(pairs, env...)
	if live == nil {
		return pairs
	}
	out := pairs[:0:0]
	for _, pair := range pairs {
		if !strings.HasPrefix(pair, workspaceenv.LivePins+"=") {
			out = append(out, pair)
		}
	}
	return append(out, live.Environment())
}

func (p *workspacePins) runner(next script.Runner) script.Runner {
	return &workspacePinRunner{pins: p, next: next}
}

type workspacePinRunner struct {
	pins *workspacePins
	next script.Runner
}

func (r *workspacePinRunner) Run(ctx context.Context, dir, command string, env []string, stdout, stderr io.Writer) error {
	return r.next.Run(ctx, dir, command, r.pins.environment(env), stdout, stderr)
}
