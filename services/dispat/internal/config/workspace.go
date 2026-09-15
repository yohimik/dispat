package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// ControlRepository is the reserved repository identity used by explicit
// cross-repository baselines and diagnostics.
const ControlRepository = "control"

// ComposeWorkspace validates and loads the repositories participating in a
// polyrepo run. Legacy configurations are returned untouched. Import paths
// passed by the CLI have already been appended to cfg.Configs and, like paths
// authored by the control file, start at the control root.
func ComposeWorkspace(cfg *File, configPath, controlRoot string, cliConfigs []string) (*Workspace, error) {
	return ComposeWorkspaceWithPins(cfg, configPath, controlRoot, cliConfigs, nil)
}

// SourcePinResolver reads the latest run-authorized revisions for one exact
// .gitmodules repository identity. Composition invokes it while holding that
// source's Git mutation lock, so a nested command compares a coherent pin and
// HEAD even while another source is recording in the same release run.
type SourcePinResolver func(repository string) ([]string, error)

// ComposeWorkspaceWithPins is ComposeWorkspace with trusted, run-local source
// revisions exported by an enclosing dispat release. It permits a nested
// command after an earlier nested commit advanced a source, while still
// requiring the checkout to equal either control HEAD or that exact exported
// revision.
func ComposeWorkspaceWithPins(cfg *File, configPath, controlRoot string, cliConfigs []string, runPins map[string][]string) (*Workspace, error) {
	return composeWorkspace(cfg, configPath, controlRoot, cliConfigs, runPins, nil)
}

// ComposeWorkspaceWithPinResolver additionally admits fresh run-scoped pins.
// Static callers keep using ComposeWorkspaceWithPins; only a CLI invocation
// that accepted an inherited workspace context supplies this resolver.
func ComposeWorkspaceWithPinResolver(cfg *File, configPath, controlRoot string, cliConfigs []string, runPins map[string][]string, resolve SourcePinResolver) (*Workspace, error) {
	return composeWorkspace(cfg, configPath, controlRoot, cliConfigs, runPins, resolve)
}

func composeWorkspace(cfg *File, configPath, controlRoot string, cliConfigs []string, runPins map[string][]string, resolve SourcePinResolver) (*Workspace, error) {
	if cfg == nil || (!cfg.Polyrepo && len(cfg.Configs) == 0 && len(cliConfigs) == 0) {
		return nil, nil
	}
	cfg.Polyrepo = true
	root, err := filepath.Abs(controlRoot)
	if err != nil {
		return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: resolve control root: %w", err))
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: resolve control root: %w", err))
	}
	if err := requireCompleteRepository(root, ControlRepository); err != nil {
		return nil, err
	}
	controlHead, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return nil, WithDiagnostic(DiagnosticRepositoryInvalid,
			fmt.Errorf("E330: polyrepo control repository has no HEAD: %w", err))
	}
	modules, err := loadSubmodules(root)
	if err != nil {
		return nil, err
	}
	repos := []Repository{{Name: ControlRepository, Root: root, ConfigPath: configPath, Config: cfg,
		Control: true, Commit: cfg.Commit, CompositionHead: controlHead}}

	seenConfig := map[string]bool{}
	if canonical, err := canonicalFile(configPath); err == nil {
		seenConfig[canonical] = true
	}
	modulesByName := make(map[string]submodule, len(modules))
	modulesByRoot := make(map[string]submodule, len(modules))
	for _, module := range modules {
		modulesByName[module.Name] = module
		modulesByRoot[module.Root] = module
	}
	importedRepo := map[string]string{}
	imports, err := workspaceImports(cfg, configPath, root, cliConfigs)
	if err != nil {
		return nil, WithDiagnostic(DiagnosticComposition, err)
	}
	for _, imp := range imports {
		declared := imp.Path
		path, err := resolveImportPath(root, imp.Base, declared)
		if err != nil {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: config %q: %w", declared, err))
		}
		canonical, err := canonicalFile(path)
		if err != nil {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: config %q: %w", declared, err))
		}
		if seenConfig[canonical] {
			continue
		}
		seenConfig[canonical] = true
		repoRoot, err := gitOutput(filepath.Dir(canonical), "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: imported config %s is not inside an initialized Git repository: %w", canonical, err))
		}
		repoRoot, err = filepath.EvalSymlinks(repoRoot)
		if err != nil {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: imported repository root %s: %w", repoRoot, err))
		}
		module, ok := modulesByRoot[repoRoot]
		if !ok {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: imported config %s belongs to %s, which is not an initialized .gitmodules repository", canonical, repoRoot))
		}
		name := module.Name
		if previous := importedRepo[name]; previous != "" && previous != canonical {
			return nil, WithDiagnostic(DiagnosticComposition, fmt.Errorf("polyrepo: repository %q has conflicting imported configs %s and %s", name, previous, canonical))
		}
		importedRepo[name] = canonical
		if err := requireCompleteRepository(repoRoot, name); err != nil {
			return nil, err
		}
		imported, err := Load(canonical, nil)
		if err != nil {
			return nil, fmt.Errorf("polyrepo: imported config %s: %w", canonical, err)
		}
		// Imports compose one level. A repository config cannot silently pull a
		// second fleet into the control run; list every participant at control.
		if len(imported.Configs) > 0 {
			return nil, WithDiagnostic(DiagnosticComposition, fmt.Errorf("polyrepo: imported config %s declares configs; nested workspace imports are not allowed", canonical))
		}
		repos = append(repos, Repository{Name: name, Root: repoRoot, GitlinkPath: module.Path, ConfigPath: canonical, Config: imported, Imported: true, Commit: imported.Commit})
	}
	for key := range cfg.RepositoryOverrides {
		module, ok := modulesByName[key]
		if !ok {
			return nil, WithDiagnostic(DiagnosticComposition, fmt.Errorf("polyrepo: repositoryOverrides names unknown source repository %q", key))
		}
		if importedRepo[module.Name] != "" {
			return nil, WithDiagnostic(DiagnosticComposition, fmt.Errorf("polyrepo: repositoryOverrides[%q] cannot override imported repository config %s", key, importedRepo[module.Name]))
		}
	}
	for _, module := range modules {
		if _, imported := importedRepo[module.Name]; imported {
			continue
		}
		commit := cfg.Commit
		if override, ok := cfg.RepositoryOverrides[module.Name]; ok && override.Commit != nil {
			commit = override.Commit
		}
		repos = append(repos, Repository{Name: module.Name, Root: module.Root, GitlinkPath: module.Path, Config: cfg, Commit: commit})
	}

	// Only initialized, pinned repositories may participate. Check all source
	// roots once here; package discovery below merely assigns packages to them.
	compositionHeads := map[string]string{ControlRepository: controlHead}
	for _, module := range modules {
		if err := requireCompleteRepository(module.Root, module.Name); err != nil {
			return nil, err
		}
		head, err := requirePinnedModuleResolved(root, controlHead, module, runPins[module.Name], resolve)
		if err != nil {
			return nil, err
		}
		compositionHeads[module.Name] = head
	}
	for i := range repos {
		repos[i].CompositionHead = compositionHeads[repos[i].Name]
	}
	if err := resolveRepositoryBaselines(cfg, repos); err != nil {
		return nil, err
	}
	workspace := newWorkspace(root, repos, modules)
	workspace.inheritedPins = resolve != nil
	return workspace, nil
}

// Repository is one participant of a composed workspace.
type Repository struct {
	Name        string
	Root        string
	GitlinkPath string
	ConfigPath  string
	Config      *File
	Control     bool
	Imported    bool
	Commit      *CommitConfig
	// CompositionHead is the exact repository HEAD accepted while the source
	// gitlink and any inherited live pin were validated. Release planning must
	// reproduce it before any hook or publication may run.
	CompositionHead string
}

// Workspace is the repository ownership map shared by every command in one
// run. It is immutable after composition.
type Workspace struct {
	ControlRoot   string
	Repositories  []Repository
	modules       []submodule
	byName        map[string]int
	byFold        map[string]int
	byRoot        map[string]int
	sourceOrder   []int
	inheritedPins bool
}

// InheritedPinsEnabled reports whether composition accepted a validated live
// run context. It prevents an explicit --root/--config invocation from later
// reopening a coincidentally matching inherited coordinator.
func (w *Workspace) InheritedPinsEnabled() bool {
	return w != nil && w.inheritedPins
}

func newWorkspace(controlRoot string, repositories []Repository, modules []submodule) *Workspace {
	w := &Workspace{
		ControlRoot: controlRoot, Repositories: repositories, modules: modules,
		byName: make(map[string]int, len(repositories)), byFold: make(map[string]int, len(repositories)),
		byRoot: make(map[string]int, len(repositories)),
	}
	for i := range repositories {
		w.byName[repositories[i].Name] = i
		w.byFold[strings.ToLower(repositories[i].Name)] = i
		w.byRoot[repositories[i].Root] = i
		if !repositories[i].Control {
			w.sourceOrder = append(w.sourceOrder, i)
		}
	}
	sort.Slice(w.sourceOrder, func(i, j int) bool {
		return pathPrefix(repositories[w.sourceOrder[i]].Root) < pathPrefix(repositories[w.sourceOrder[j]].Root)
	})
	return w
}

// RepositoryForPackage returns the source repository owning p.
func (w *Workspace) RepositoryForPackage(p *model.Package) *Repository {
	if w == nil || p == nil {
		return nil
	}
	if i, ok := w.byName[p.Repository]; ok {
		return &w.Repositories[i]
	}
	for i := range w.Repositories {
		if w.Repositories[i].Name == p.Repository {
			return &w.Repositories[i]
		}
	}
	return nil
}

// RepositoryByName returns one participating repository by its stable
// .gitmodules identity (or the reserved control identity).
func (w *Workspace) RepositoryByName(name string) *Repository {
	if w == nil {
		return nil
	}
	if i, ok := w.byFold[strings.ToLower(name)]; ok {
		return &w.Repositories[i]
	}
	for i := range w.Repositories {
		if strings.EqualFold(w.Repositories[i].Name, name) {
			return &w.Repositories[i]
		}
	}
	return nil
}

// ConfigurationForPackage returns the config that declares p. Imported
// packages belong to their repository-local config; centrally managed
// packages remain declarations of the control config even though their files
// and history belong to a source repository.
func (w *Workspace) ConfigurationForPackage(p *model.Package) *Repository {
	if repo := w.RepositoryForPackage(p); repo != nil && repo.Imported {
		return repo
	}
	return w.RepositoryByName(ControlRepository)
}

// RepositoryForDir returns the deepest participating repository containing
// dir. The deepest rule is useful for the control repository, which contains
// all of its source submodules as filesystem paths.
func (w *Workspace) RepositoryForDir(dir string) *Repository {
	if w == nil {
		return nil
	}
	dir, _ = filepath.Abs(dir)
	if canonical, err := filepath.EvalSymlinks(dir); err == nil {
		dir = canonical
	}
	return w.repositoryForCanonicalDir(dir)
}

func (w *Workspace) repositoryForCanonicalDir(dir string) *Repository {
	if len(w.byRoot) == 0 {
		var best *Repository
		for i := range w.Repositories {
			repo := &w.Repositories[i]
			if within(repo.Root, dir) && (best == nil || len(repo.Root) > len(best.Root)) {
				best = repo
			}
		}
		return best
	}
	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		if i, ok := w.byRoot[current]; ok {
			return &w.Repositories[i]
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func pathPrefix(path string) string {
	return strings.TrimRight(filepath.Clean(path), string(filepath.Separator)) + string(filepath.Separator)
}

func (w *Workspace) sourceWithinCanonicalDir(dir string) *Repository {
	if len(w.sourceOrder) == 0 {
		for i := range w.Repositories {
			repo := &w.Repositories[i]
			if !repo.Control && within(dir, repo.Root) {
				return repo
			}
		}
		return nil
	}
	prefix := pathPrefix(dir)
	i := sort.Search(len(w.sourceOrder), func(i int) bool {
		return pathPrefix(w.Repositories[w.sourceOrder[i]].Root) >= prefix
	})
	if i < len(w.sourceOrder) {
		repo := &w.Repositories[w.sourceOrder[i]]
		if strings.HasPrefix(pathPrefix(repo.Root), prefix) {
			return repo
		}
	}
	return nil
}

type submodule struct {
	Name string
	Path string
	Root string
}

func loadSubmodules(controlRoot string) ([]submodule, error) {
	file := filepath.Join(controlRoot, ".gitmodules")
	if _, err := os.Stat(file); err != nil {
		if os.IsNotExist(err) {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: %s has no .gitmodules", controlRoot))
		}
		return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: read .gitmodules: %w", err))
	}
	out, err := gitOutput(controlRoot, "config", "--file", file, "--null", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: read .gitmodules: %w", err))
	}
	var modules []submodule
	seen := map[string]bool{}
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		key, path, ok := strings.Cut(record, "\n")
		if !ok {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: malformed git config output for .gitmodules"))
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")
		if name == "" || strings.EqualFold(name, ControlRepository) {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: invalid or reserved submodule name %q", name))
		}
		if seen[strings.ToLower(name)] {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: duplicate submodule name %q (names are case-insensitive)", name))
		}
		seen[strings.ToLower(name)] = true
		abs, err := containedPath(controlRoot, path)
		if err != nil {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: submodule %q path %q: %w", name, path, err))
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: submodule %q is missing or uninitialized at %s", name, abs))
		}
		if resolved == controlRoot || !within(controlRoot, resolved) {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: submodule %q path %s resolves outside the control workspace", name, resolved))
		}
		gitRoot, err := gitOutput(resolved, "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: submodule %q is not initialized at %s", name, abs))
		}
		gitRoot, _ = filepath.EvalSymlinks(gitRoot)
		if gitRoot != resolved {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: submodule %q path %s resolves to Git root %s", name, resolved, gitRoot))
		}
		modules = append(modules, submodule{Name: name, Path: filepath.ToSlash(filepath.Clean(path)), Root: resolved})
	}
	byRoot := append([]submodule(nil), modules...)
	separator := string(filepath.Separator)
	sort.Slice(byRoot, func(i, j int) bool {
		a := strings.TrimRight(filepath.Clean(byRoot[i].Root), separator) + separator
		b := strings.TrimRight(filepath.Clean(byRoot[j].Root), separator) + separator
		return a < b
	})
	for i := 1; i < len(byRoot); i++ {
		previous := strings.TrimRight(filepath.Clean(byRoot[i-1].Root), separator) + separator
		current := strings.TrimRight(filepath.Clean(byRoot[i].Root), separator) + separator
		if strings.HasPrefix(current, previous) {
			return nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: submodule roots overlap between %q (%s) and %q (%s)",
				byRoot[i-1].Name, byRoot[i-1].Root, byRoot[i].Name, byRoot[i].Root))
		}
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Name < modules[j].Name })
	return modules, nil
}

func requireCompleteRepository(root, name string) error {
	shallow, err := gitOutput(root, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: repository %q at %s is not initialized: %w", name, root, err))
	}
	if shallow == "true" {
		return WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: repository %q at %s is shallow; complete history is required", name, root))
	}
	return nil
}

func requirePinnedModule(controlRoot, controlRevision string, module submodule, runPins []string) (string, error) {
	pinned, err := gitOutput(controlRoot, "rev-parse", controlRevision+":"+module.Path)
	if err != nil {
		return "", WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: source repository %q is not pinned by control HEAD: %w", module.Name, err))
	}
	head, err := gitOutput(module.Root, "rev-parse", "HEAD")
	if err != nil {
		return "", WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: source repository %q has no HEAD: %w", module.Name, err))
	}
	if pinned != head {
		for _, pin := range runPins {
			if pin == head {
				return head, nil
			}
		}
		return "", WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("E330: polyrepo source repository %q is checked out at %s but control HEAD pins %s", module.Name, head, pinned))
	}
	return head, nil
}

func requirePinnedModuleResolved(controlRoot, controlRevision string, module submodule, runPins []string, resolve SourcePinResolver) (string, error) {
	if resolve == nil {
		return requirePinnedModule(controlRoot, controlRevision, module, runPins)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	git := &gitx.CLI{Dir: module.Root}
	unlock, err := git.AcquireMutation(ctx)
	if err != nil {
		return "", WithDiagnostic(DiagnosticRepositoryInvalid,
			fmt.Errorf("E330: polyrepo source repository %q: acquiring live pin validation lock: %w", module.Name, err))
	}
	defer unlock()
	fresh, err := resolve(module.Name)
	if err != nil {
		return "", WithDiagnostic(DiagnosticRepositoryInvalid,
			fmt.Errorf("E330: polyrepo source repository %q: reading live run pin: %w", module.Name, err))
	}
	pins := append(append([]string(nil), runPins...), fresh...)
	return requirePinnedModule(controlRoot, controlRevision, module, pins)
}

func resolveRepositoryBaselines(cfg *File, repos []Repository) error {
	byName := make(map[string]Repository, len(repos))
	for _, repo := range repos {
		byName[repo.Name] = repo
	}
	seen := map[string]RepositoryBaselineConfig{}
	for i := range cfg.RepositoryBaselines {
		b := &cfg.RepositoryBaselines[i]
		where := fmt.Sprintf("repositoryBaselines[%d]", i)
		if strings.TrimSpace(b.Consumer) == "" || strings.TrimSpace(b.ReleaseTag) == "" ||
			strings.TrimSpace(b.Repository) == "" || strings.TrimSpace(b.Revision) == "" {
			return WithDiagnostic(DiagnosticBoundary, fmt.Errorf("config: %s: consumer, releaseTag, repository and revision are required", where))
		}
		key := strings.ToLower(b.Consumer) + "\x00" + b.ReleaseTag + "\x00" + b.Repository
		if previous, ok := seen[key]; ok {
			return WithDiagnostic(DiagnosticBoundary, fmt.Errorf("config: %s duplicates baseline for consumer %q and releaseTag %q (previous repository %q revision %q)",
				where, b.Consumer, b.ReleaseTag, previous.Repository, previous.Revision))
		}
		repo, ok := byName[b.Repository]
		if !ok {
			return WithDiagnostic(DiagnosticBoundary, fmt.Errorf("config: %s: unknown repository %q", where, b.Repository))
		}
		oid, err := gitOutput(repo.Root, "rev-parse", "--verify", b.Revision+"^{commit}")
		if err != nil {
			return WithDiagnostic(DiagnosticBoundary, fmt.Errorf("config: %s: revision %q is not a commit in repository %q", where, b.Revision, repo.Name))
		}
		if _, err := gitOutput(repo.Root, "merge-base", "--is-ancestor", oid, "HEAD"); err != nil {
			return WithDiagnostic(DiagnosticBoundary, fmt.Errorf("config: %s: revision %q is not reachable from repository %q HEAD", where, b.Revision, repo.Name))
		}
		b.Repository = repo.Name
		b.Revision = oid
		seen[key] = *b
	}
	return nil
}

func DiscoverWorkspace(c *File, controlRoot string, workspace *Workspace) ([]*model.Package, []model.Dependency, []ExcludedDir, error) {
	pkgs, active, _, excluded, err := DiscoverWorkspacePlan(c, controlRoot, workspace)
	return pkgs, active, excluded, err
}

// DiscoverWorkspacePlan returns both active dependency edges and declared
// external edges whose provider is absent from this snapshot. Planning keeps
// the latter out of the graph but reports their inactive state explicitly.
func DiscoverWorkspacePlan(c *File, controlRoot string, workspace *Workspace) ([]*model.Package, []model.Dependency, []model.Dependency, []ExcludedDir, error) {
	pkgs, declared, excluded, err := DiscoverWorkspacePackages(c, controlRoot, workspace)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	active, inactive, err := validateDependenciesForPlan(pkgs, declared)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return pkgs, active, inactive, excluded, nil
}

// DiscoverWorkspacePackages is the composed equivalent of DiscoverPackages:
// it retains invalid or stale dependency declarations for compute to repair.
func DiscoverWorkspacePackages(c *File, controlRoot string, workspace *Workspace) ([]*model.Package, []DeclaredDependency, []ExcludedDir, error) {
	if workspace == nil {
		return DiscoverPackages(c, controlRoot)
	}
	var pkgs []*model.Package
	var declared []DeclaredDependency
	var excluded []ExcludedDir
	gitRoots := &gitRootMemo{byDir: make(map[string]string)}
	for _, repo := range workspace.Repositories {
		if !repo.Control && !repo.Imported {
			continue
		}
		var local []*model.Package
		var deps []DeclaredDependency
		var ex []ExcludedDir
		var err error
		if repo.Control {
			local, deps, ex, err = discoverCentralPackages(repo.Config, repo.Root)
		} else {
			local, deps, ex, err = DiscoverPackages(repo.Config, repo.Root)
		}
		if err != nil {
			return nil, nil, nil, err
		}
		for i := range deps {
			deps[i].Source.Repository = repo.Name
		}
		for _, p := range local {
			owner := repo.Name
			ownerRoot := repo.Root
			if repo.Control {
				owner, ownerRoot, err = packageRepository(p, workspace, gitRoots)
				if err != nil {
					return nil, nil, nil, err
				}
			} else {
				dir, scope, resolveErr := canonicalPackagePaths(p)
				if resolveErr != nil {
					return nil, nil, nil, resolveErr
				}
				dirOwner, scopeOwner := workspace.repositoryForCanonicalDir(dir), workspace.repositoryForCanonicalDir(scope)
				if dirOwner == nil || scopeOwner == nil || dirOwner.Name != repo.Name || scopeOwner.Name != repo.Name {
					return nil, nil, nil, WithDiagnostic(DiagnosticOwnershipInvalid, fmt.Errorf("polyrepo: imported repository %q package %q path or src escapes its owner root %s", repo.Name, p.Name, repo.Root))
				}
				if err := gitRoots.requireOwner(p, dir, scope, &repo); err != nil {
					return nil, nil, nil, err
				}
				p.Dir = dir
			}
			p.Repository, p.RepoRoot = owner, ownerRoot
			if p.Space != nil && !repo.Control {
				p.Space.Repository, p.Space.RepoRoot = owner, ownerRoot
				if p.Space.Versioning.Shared() {
					group := p.Space.VersionGroup
					if group == "" {
						group = p.Space.Name
					}
					p.Space.GroupIdentity = repo.Name + "\x00" + group
				}
			}
			pkgs = append(pkgs, p)
		}
		declared = append(declared, deps...)
		excluded = append(excluded, ex...)
	}
	if err := validatePackageOwnership(pkgs); err != nil {
		return nil, nil, nil, err
	}
	return pkgs, declared, excluded, nil
}

func canonicalPackagePaths(p *model.Package) (string, string, error) {
	dir, err := filepath.EvalSymlinks(p.Dir)
	if err != nil {
		return "", "", WithDiagnostic(DiagnosticOwnershipInvalid, fmt.Errorf("polyrepo: package %q path %s: %w", p.Name, p.Dir, err))
	}
	if p.Src == "" {
		return dir, dir, nil
	}
	scope, err := filepath.EvalSymlinks(p.ScopeDir())
	if err != nil {
		return "", "", WithDiagnostic(DiagnosticOwnershipInvalid, fmt.Errorf("polyrepo: package %q src path %s: %w", p.Name, p.ScopeDir(), err))
	}
	return dir, scope, nil
}

func packageRepository(p *model.Package, workspace *Workspace, gitRoots *gitRootMemo) (string, string, error) {
	dir, scope, err := canonicalPackagePaths(p)
	if err != nil {
		return "", "", err
	}
	dirOwner := workspace.repositoryForCanonicalDir(dir)
	scopeOwner := workspace.repositoryForCanonicalDir(scope)
	if dirOwner == nil {
		return "", "", WithDiagnostic(DiagnosticOwnershipInvalid, fmt.Errorf("polyrepo: package %q path %s escapes the control workspace", p.Name, p.Dir))
	}
	if scopeOwner == nil || scopeOwner != dirOwner {
		return "", "", WithDiagnostic(DiagnosticOwnershipInvalid, fmt.Errorf("polyrepo: package %q src path %s crosses repository ownership from %q", p.Name, p.ScopeDir(), dirOwner.Name))
	}
	// A control-owned package cannot wrap a source checkout. Its file scope
	// would otherwise cross histories, and an ordinary control commit could
	// ambiguously claim files whose commit object belongs to a source.
	if dirOwner.Control {
		if source := workspace.sourceWithinCanonicalDir(dir); source != nil {
			return "", "", WithDiagnostic(DiagnosticOwnershipInvalid, fmt.Errorf("polyrepo: package %q path %s spans source repository %q at %s", p.Name, p.Dir, source.Name, source.Root))
		}
	}
	if err := gitRoots.requireOwner(p, dir, scope, dirOwner); err != nil {
		return "", "", err
	}
	p.Dir = dir
	return dirOwner.Name, dirOwner.Root, nil
}

type gitRootMemo struct {
	byDir map[string]string
}

func (m *gitRootMemo) requireOwner(p *model.Package, dir, scope string, owner *Repository) error {
	for _, candidate := range []struct {
		label string
		path  string
	}{{"path", dir}, {"src path", scope}} {
		actual, err := m.nearest(candidate.path)
		if err != nil {
			return WithDiagnostic(DiagnosticOwnershipInvalid, fmt.Errorf("polyrepo: package %q %s %s: %w", p.Name, candidate.label, candidate.path, err))
		}
		if actual != owner.Root {
			return WithDiagnostic(DiagnosticOwnershipInvalid, fmt.Errorf("polyrepo: package %q %s %s belongs to unlisted nested Git repository %s, not repository %q at %s", p.Name, candidate.label, candidate.path, actual, owner.Name, owner.Root))
		}
	}
	return nil
}

// nearest finds the nearest Git worktree marker without starting one Git
// process per package. Every traversed ancestor is memoized for the remaining
// discovery pass. Both normal `.git` directories and worktree/submodule
// `.git` files are repositories; symlinked markers are rejected.
func (m *gitRootMemo) nearest(path string) (string, error) {
	current := filepath.Clean(path)
	var traversed []string
	for {
		if root, ok := m.byDir[current]; ok {
			for _, dir := range traversed {
				m.byDir[dir] = root
			}
			return root, nil
		}
		traversed = append(traversed, current)
		info, err := os.Lstat(filepath.Join(current, ".git"))
		switch {
		case err == nil && (info.IsDir() || info.Mode().IsRegular()):
			for _, dir := range traversed {
				m.byDir[dir] = current
			}
			return current, nil
		case err == nil:
			return "", fmt.Errorf("unsupported .git marker at %s", current)
		case !os.IsNotExist(err):
			return "", fmt.Errorf("inspect .git marker at %s: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no Git repository contains %s", path)
		}
		current = parent
	}
}

func validatePackageOwnership(pkgs []*model.Package) error {
	byName := map[string]*model.Package{}
	for _, p := range pkgs {
		fold := strings.ToLower(p.Name)
		if previous := byName[fold]; previous != nil {
			return WithDiagnostic(DiagnosticComposition, fmt.Errorf("polyrepo: duplicate package name %q in repositories %q and %q (names are case-insensitive)", p.Name, previous.Repository, p.Repository))
		}
		byName[fold] = p
	}
	// A directory prefix sorts immediately before its descendants when the
	// separator is part of the key. Adjacent comparisons then detect every
	// overlap without comparing every pair of packages.
	type scope struct {
		pkg *model.Package
		key string
	}
	scopes := make([]scope, len(pkgs))
	separator := string(filepath.Separator)
	for i, p := range pkgs {
		resolved, err := filepath.EvalSymlinks(p.ScopeDir())
		if err != nil {
			return WithDiagnostic(DiagnosticOwnershipInvalid, fmt.Errorf("polyrepo: package %q src path %s: %w", p.Name, p.ScopeDir(), err))
		}
		key := strings.TrimRight(filepath.Clean(resolved), separator) + separator
		scopes[i] = scope{pkg: p, key: key}
	}
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].key < scopes[j].key })
	for i := 1; i < len(scopes); i++ {
		if strings.HasPrefix(scopes[i].key, scopes[i-1].key) {
			a, b := scopes[i-1].pkg, scopes[i].pkg
			return WithDiagnostic(DiagnosticOwnershipInvalid, fmt.Errorf("polyrepo: package ownership overlaps between %q (%s) and %q (%s)", a.Name, a.ScopeDir(), b.Name, b.ScopeDir()))
		}
	}
	return nil
}

func containedPath(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(path))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !within(root, abs) {
		return "", fmt.Errorf("path escapes control root %s", root)
	}
	return abs, nil
}

func resolveImportPath(controlRoot, declaringDir, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(declaringDir, filepath.FromSlash(path))
	}
	return containedPath(controlRoot, path)
}

type workspaceImport struct {
	Path string
	Base string
}

func workspaceImports(cfg *File, configPath, controlRoot string, cli []string) ([]workspaceImport, error) {
	authored, found, err := resolveWorkspaceImports(configPath, 0)
	if err != nil {
		return nil, fmt.Errorf("polyrepo: resolve configs declaration: %w", err)
	}
	if !found || !sameWorkspaceImportValues(authored, cfg.Configs) {
		authored = make([]workspaceImport, len(cfg.Configs))
		for i, item := range cfg.Configs {
			authored[i] = workspaceImport{Path: item, Base: filepath.Dir(configPath)}
		}
	}
	for i := range authored {
		base, err := filepath.Abs(authored[i].Base)
		if err == nil {
			base, err = filepath.EvalSymlinks(base)
		}
		if err != nil {
			return nil, fmt.Errorf("polyrepo: resolve configs declaration directory: %w", err)
		}
		authored[i].Base = base
	}
	for _, item := range cli {
		authored = append(authored, workspaceImport{Path: item, Base: controlRoot})
	}
	return authored, nil
}

func sameWorkspaceImportValues(imports []workspaceImport, values []string) bool {
	if len(imports) != len(values) {
		return false
	}
	for i := range imports {
		if imports[i].Path != values[i] {
			return false
		}
	}
	return true
}

// resolveWorkspaceImports returns each configured path with the directory of
// the file that contributed that list element. Root object references merge
// by key, so a direct configs key wins and the last referenced object carrying
// the key wins otherwise. A reference used as the configs value may merge
// lists; those entries keep the directory of their individual list file.
func resolveWorkspaceImports(path string, depth int) ([]workspaceImport, bool, error) {
	if depth > maxRefDepth {
		return nil, false, fmt.Errorf("$ref nesting is more than %d files deep at %s", maxRefDepth, path)
	}
	doc, err := readRawWorkspaceConfig(path)
	if err != nil {
		return nil, false, err
	}
	node, ok := doc.(map[string]any)
	if !ok {
		return nil, false, nil
	}
	if value, ok := lookupFold(node, "configs"); ok && value != nil {
		imports, err := resolveWorkspaceImportValue(value, path, depth+1)
		return imports, true, err
	}
	targets, err := workspaceRefTargets(node["$ref"])
	if err != nil {
		return nil, false, err
	}
	for i := len(targets) - 1; i >= 0; i-- {
		target := workspaceRefPath(path, targets[i])
		if imports, found, err := resolveWorkspaceImports(target, depth+1); err != nil {
			return nil, false, err
		} else if found {
			return imports, true, nil
		}
	}
	return nil, false, nil
}

func resolveWorkspaceImportValue(value any, declaringFile string, depth int) ([]workspaceImport, error) {
	if node, ok := value.(map[string]any); ok {
		targets, err := workspaceRefTargets(node["$ref"])
		if err != nil {
			return nil, err
		}
		var out []workspaceImport
		for _, target := range targets {
			items, err := resolveWorkspaceImportDocument(workspaceRefPath(declaringFile, target), depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, items...)
		}
		return out, nil
	}
	return workspaceImportItems(value, declaringFile)
}

func resolveWorkspaceImportDocument(path string, depth int) ([]workspaceImport, error) {
	if depth > maxRefDepth {
		return nil, fmt.Errorf("$ref nesting is more than %d files deep at %s", maxRefDepth, path)
	}
	doc, err := readRawWorkspaceConfig(path)
	if err != nil {
		return nil, err
	}
	return resolveWorkspaceImportValue(doc, path, depth)
}

func workspaceImportItems(value any, declaringFile string) ([]workspaceImport, error) {
	base := filepath.Dir(declaringFile)
	if item, ok := value.(string); ok {
		values := splitList(item)
		out := make([]workspaceImport, len(values))
		for i := range values {
			out[i] = workspaceImport{Path: values[i], Base: base}
		}
		return out, nil
	}
	items, ok := weakList(value)
	if !ok {
		item, err := weakString(value, "configs")
		if err != nil {
			return nil, err
		}
		return []workspaceImport{{Path: item, Base: base}}, nil
	}
	out := make([]workspaceImport, len(items))
	for i := range items {
		item, err := weakString(items[i], fmt.Sprintf("configs[%d]", i))
		if err != nil {
			return nil, err
		}
		out[i] = workspaceImport{Path: item, Base: base}
	}
	return out, nil
}

func readRawWorkspaceConfig(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	formats := dispatFormats()
	parse, ok := formats[strings.ToLower(filepath.Ext(path))]
	if !ok {
		parse = formats[""]
	}
	return parse(data)
}

func workspaceRefPath(declaringFile, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Join(filepath.Dir(declaringFile), filepath.FromSlash(target))
}

func workspaceRefTargets(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	if target, ok := value.(string); ok {
		return []string{target}, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("$ref must name a file or list of files")
	}
	targets := make([]string, len(items))
	for i, item := range items {
		target, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("$ref[%d] must name a file", i)
		}
		targets[i] = target
	}
	return targets, nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func canonicalFile(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
	}
	return strings.TrimSpace(string(out)), nil
}
