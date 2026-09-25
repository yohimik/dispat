package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// ControlRepository is the reserved repository identity used by explicit
// cross-repository baselines and diagnostics.
const ControlRepository = "control"

// SourcePinResolver reads the latest run-authorized revisions for one exact
// .gitmodules repository identity. Composition reads it before and after the
// source's HEAD and accepts only a pair the two reads agree on, so a nested
// command compares a coherent pin and HEAD even while the release around it is
// recording in that source.
type SourcePinResolver func(repository string) ([]string, error)

// ComposeWorkspaceWithPinResolver validates and loads the repositories in a
// workspace and admits trusted run-scoped pins supplied by an enclosing run.
// Legacy configurations return no workspace. Paths authored by the control
// file and CLI imports start at their respective declaring roots.
//
// ctx is the invocation's own cancellable context. Validating a live pin can
// wait a few seconds for the release around the command to publish a revision
// it has just committed, so the wait has to be interruptible: an operator who
// presses Ctrl-C while composition waits must stop.
func ComposeWorkspaceWithPinResolver(ctx context.Context, cfg *File, configPath, controlRoot string, cliConfigs []string, runPins map[string][]string, resolve SourcePinResolver) (*Workspace, error) {
	return composeWorkspace(ctx, cfg, configPath, controlRoot, cliConfigs, runPins, resolve, false)
}

// ComposeWorkspaceForRepair composes what it can and reports the rest as
// findings, for `dispat compute`: the command whose whole purpose is to
// repair a fleet that does not compose yet cannot need it to compose first.
// Every other command gets the strict composition, because a plan computed
// over half a fleet would be wrong rather than incomplete.
func ComposeWorkspaceForRepair(ctx context.Context, cfg *File, configPath, controlRoot string, cliConfigs []string, runPins map[string][]string, resolve SourcePinResolver) (*Workspace, error) {
	return composeWorkspace(ctx, cfg, configPath, controlRoot, cliConfigs, runPins, resolve, true)
}

func composeWorkspace(ctx context.Context, cfg *File, configPath, controlRoot string, cliConfigs []string,
	runPins map[string][]string, resolve SourcePinResolver, lenient bool) (*Workspace, error) {
	// A linked fleet composes by following links rather than by
	// reading one repository's inventory, so it is a different walk to the
	// same result. Stating a repository identity sets the polyrepo flag, and
	// `--polyrepo=false` clears it again to release one peer on its own,
	// which is why the delegation asks for both.
	if cfg != nil && cfg.IsLinked() && cfg.Polyrepo {
		if len(cliConfigs) > 0 {
			return nil, WithDiagnostic(DiagnosticComposition, fmt.Errorf(
				"linked fleet: --configs imports a repository-local configuration into a control run; every peer carries its own"))
		}
		return ComposeLinked(ctx, cfg, configPath, controlRoot,
			LinkedOptions{InheritedPins: resolve != nil, Lenient: lenient})
	}
	if cfg == nil || (!cfg.Polyrepo && len(cfg.Configs) == 0 && len(cliConfigs) == 0) {
		return nil, nil
	}
	cfg.Polyrepo = true
	root, controlHead, err := resolveControlRepository(controlRoot)
	if err != nil {
		return nil, err
	}
	modules, disabled, err := loadSubmodules(root, cfg.RepositoryOverrides)
	if err != nil {
		return nil, err
	}
	// Participation settles before anything else: the control declarations an
	// excluded repository owns are removed here, ahead of source
	// initialization, history, pins, package discovery, hooks and locks.
	participants := resolveParticipation(cfg, root, disabled)
	c := centralComposition{cfg: cfg, configPath: configPath, root: root, controlHead: controlHead,
		modules: modules, participants: participants}
	repos := []Repository{{Name: ControlRepository, Root: root, ConfigPath: configPath, Config: cfg,
		Control: true, Entry: true, Commit: cfg.Commit, CompositionHead: controlHead}}
	imported, importedRepo, err := c.importRepositories(cliConfigs)
	if err != nil {
		return nil, err
	}
	repos = append(repos, imported...)
	if err := c.checkRepositoryOverrides(importedRepo); err != nil {
		return nil, err
	}
	repos = append(repos, c.sourceRepositories(importedRepo)...)
	if err := c.resolveCompositionHeads(ctx, repos, runPins, resolve); err != nil {
		return nil, err
	}
	if err := resolveRepositoryBaselines(cfg, baselineResolution{repos: repos, participants: participants}); err != nil {
		return nil, err
	}
	workspace := newWorkspace(root, repos, modules)
	workspace.disabled = participants.disabled
	workspace.excludedPackages = participants.packages
	workspace.excludedSpaces = participants.spaces
	workspace.inheritedPins = resolve != nil
	return workspace, nil
}

// centralComposition is what the steps of a central composition share: the
// control configuration and repository, the source repositories .gitmodules
// declares and the participation settled over them.
type centralComposition struct {
	cfg          *File
	configPath   string
	root         string
	controlHead  string
	modules      []submodule
	participants *participation
}

// resolveControlRepository returns the control repository's root, resolved
// through any symbolic link, and the HEAD the composition is taken at.
func resolveControlRepository(controlRoot string) (string, string, error) {
	root, err := filepath.Abs(controlRoot)
	if err != nil {
		return "", "", WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: resolve control root: %w", err))
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: resolve control root: %w", err))
	}
	if err := requireCompleteRepository(root, ControlRepository); err != nil {
		return "", "", err
	}
	controlHead, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", WithDiagnostic(DiagnosticRepositoryInvalid,
			fmt.Errorf("E330: polyrepo control repository has no HEAD: %w", err))
	}
	return root, controlHead, nil
}

// importRepositories loads the repository-local configurations the control
// file and the command line import, each one a source repository's own, and
// returns them with the canonical configuration path of each imported
// repository.
func (c centralComposition) importRepositories(cliConfigs []string) ([]Repository, map[string]string, error) {
	seenConfig := map[string]bool{}
	if canonical, err := canonicalFile(c.configPath); err == nil {
		seenConfig[canonical] = true
	}
	modulesByRoot := make(map[string]submodule, len(c.modules))
	for _, module := range c.modules {
		modulesByRoot[module.Root] = module
	}
	importedRepo := map[string]string{}
	imports, err := workspaceImports(c.cfg, c.configPath, c.root, cliConfigs)
	if err != nil {
		return nil, nil, WithDiagnostic(DiagnosticComposition, err)
	}
	var repos []Repository
	for _, imp := range imports {
		declared := imp.Path
		path, err := resolveImportPath(c.root, imp.Base, declared)
		if err != nil {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: config %q: %w", declared, err))
		}
		if c.participants.ownerOf(path) != nil {
			continue
		}
		canonical, err := canonicalFile(path)
		if err != nil {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: config %q: %w", declared, err))
		}
		if seenConfig[canonical] {
			continue
		}
		seenConfig[canonical] = true
		repoRoot, err := gitOutput(filepath.Dir(canonical), "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: imported config %s is not inside an initialized Git repository: %w", canonical, err))
		}
		repoRoot, err = filepath.EvalSymlinks(repoRoot)
		if err != nil {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: imported repository root %s: %w", repoRoot, err))
		}
		if !within(repoRoot, canonical) {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf(
				"polyrepo: Git root %s does not contain imported config %s", repoRoot, canonical))
		}
		module, ok := modulesByRoot[repoRoot]
		if !ok {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: imported config %s belongs to %s, which is not an initialized .gitmodules repository", canonical, repoRoot))
		}
		name := module.Name
		if previous := importedRepo[name]; previous != "" && previous != canonical {
			return nil, nil, WithDiagnostic(DiagnosticComposition, fmt.Errorf("polyrepo: repository %q has conflicting imported configs %s and %s", name, previous, canonical))
		}
		importedRepo[name] = canonical
		if err := requireCompleteRepository(repoRoot, name); err != nil {
			return nil, nil, err
		}
		imported, err := Load(canonical, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("polyrepo: imported config %s: %w", canonical, err)
		}
		// Imports compose one level. A repository config cannot silently pull a
		// second fleet into the control run; list every participant at control.
		if len(imported.Configs) > 0 {
			return nil, nil, WithDiagnostic(DiagnosticComposition, fmt.Errorf("polyrepo: imported config %s declares configs; nested workspace imports are not allowed", canonical))
		}
		repos = append(repos, Repository{Name: name, Root: repoRoot, GitlinkPath: module.Path, ConfigPath: canonical, Config: imported, Imported: true, Commit: imported.Commit})
	}
	return repos, importedRepo, nil
}

// checkRepositoryOverrides refuses a commit override of a repository whose own
// imported configuration states its commit settings. An override naming no
// source repository never reaches it: loadSubmodules refused that name
// against the declared inventory.
func (c centralComposition) checkRepositoryOverrides(importedRepo map[string]string) error {
	for _, module := range c.modules {
		override, ok := c.cfg.RepositoryOverrides[module.Name]
		if !ok || !override.IsEnabled() {
			continue
		}
		if importedRepo[module.Name] != "" && override.Commit != nil {
			return WithDiagnostic(DiagnosticComposition, fmt.Errorf("polyrepo: repositoryOverrides[%q] cannot override imported repository config %s", module.Name, importedRepo[module.Name]))
		}
	}
	return nil
}

// sourceRepositories are the source repositories no import configures: they
// run under the control file, with its commit settings unless an override
// states their own.
func (c centralComposition) sourceRepositories(importedRepo map[string]string) []Repository {
	var repos []Repository
	for _, module := range c.modules {
		if _, imported := importedRepo[module.Name]; imported {
			continue
		}
		commit := c.cfg.Commit
		if override, ok := c.cfg.RepositoryOverrides[module.Name]; ok && override.Commit != nil {
			commit = override.Commit
		}
		repos = append(repos, Repository{Name: module.Name, Root: module.Root, GitlinkPath: module.Path, Config: c.cfg, Commit: commit})
	}
	return repos
}

// resolveCompositionHeads checks every source repository is initialized and
// pinned, and records on each participant the HEAD composition accepted.
func (c centralComposition) resolveCompositionHeads(ctx context.Context, repos []Repository, runPins map[string][]string, resolve SourcePinResolver) error {
	// Only initialized, pinned repositories may participate. Check all source
	// roots once here; package discovery below merely assigns packages to them.
	compositionHeads := map[string]string{ControlRepository: c.controlHead}
	for _, module := range c.modules {
		if err := requireCompleteRepository(module.Root, module.Name); err != nil {
			return err
		}
		head, err := requirePinnedModuleResolved(ctx, c.root, c.controlHead, module, runPins[module.Name], resolve)
		if err != nil {
			return err
		}
		compositionHeads[module.Name] = head
	}
	for i := range repos {
		repos[i].CompositionHead = compositionHeads[repos[i].Name]
	}
	return nil
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
	// Entry marks the repository the command was invoked in: the control
	// repository of a central fleet, and the peer a linked run
	// started from, which is any of them.
	Entry bool
	// Linker is the identity of the repository whose fleet link reached this
	// one, in a linked fleet. The entry has none.
	Linker string
	// Links maps each fleet peer this repository links to the gitlink path
	// holding it, relative to this repository's root. Orchestration links
	// nothing: its inventory is the control repository's `.gitmodules`.
	Links map[string]string
}

// Workspace is the repository ownership map shared by every command in one
// run. It is immutable after composition.
type Workspace struct {
	// ControlRoot is the root of the repository the run is anchored in: the
	// control repository of a central fleet, and the entry repository of
	// a linked one, which owns no other repository's configuration.
	ControlRoot  string
	Repositories []Repository
	// Findings are the recoverable link problems composition observed.
	// LinkFindings is the nil-safe way to read them.
	Findings      []LinkFinding
	modules       []submodule
	index         workspaceIndex
	inheritedPins bool
	// disabled retains the boundaries of the repositories this run excluded.
	disabled []DisabledRepository
	// excludedPackages attributes a package name the exclusion removed to the
	// repository that owned it, for diagnostics alone.
	excludedPackages map[string]string
	// excludedSpaces names the control space declarations the exclusion
	// removed, for the composition log.
	excludedSpaces []string
}

// ExcludedSpaces returns the control space declarations that stopped
// contributing because an excluded repository owned every path they named.
func (w *Workspace) ExcludedSpaces() []string {
	if w == nil {
		return nil
	}
	return w.excludedSpaces
}

// SetParserQuietOverride applies the invocation's parser display override to
// every participating repository configuration.
func (w *Workspace) SetParserQuietOverride(quiet bool) {
	if w == nil {
		return
	}
	for i := range w.Repositories {
		if w.Repositories[i].Config.Parser == nil {
			w.Repositories[i].Config.Parser = &ParserConfig{}
		}
		w.Repositories[i].Config.Parser.Quiet = quiet
	}
}

// IsInheritedPinsEnabled reports whether composition accepted a validated live
// run context. It prevents an explicit --root/--config invocation from later
// reopening a coincidentally matching inherited coordinator.
func (w *Workspace) IsInheritedPinsEnabled() bool {
	return w != nil && w.inheritedPins
}

func newWorkspace(controlRoot string, repositories []Repository, modules []submodule) *Workspace {
	return &Workspace{ControlRoot: controlRoot, Repositories: repositories, modules: modules}
}

// workspaceIndex is the lookup a workspace answers its repository questions
// from: by exact name, by folded name, by root, and the source repositories in
// root order. It is built once, on the first question, from the repositories
// the workspace holds by then, whoever assembled it.
type workspaceIndex struct {
	once        sync.Once
	byName      map[string]int
	byFold      map[string]int
	byRoot      map[string]int
	sourceOrder []int
}

// indexed answers the workspace's index, building it on the first call.
func (w *Workspace) indexed() *workspaceIndex {
	w.index.once.Do(func() {
		repositories := w.Repositories
		index := &w.index
		index.byName = make(map[string]int, len(repositories))
		index.byFold = make(map[string]int, len(repositories))
		index.byRoot = make(map[string]int, len(repositories))
		for i := range repositories {
			index.byName[repositories[i].Name] = i
			index.byFold[globx.Fold(repositories[i].Name)] = i
			index.byRoot[repositories[i].Root] = i
			if !repositories[i].Control {
				index.sourceOrder = append(index.sourceOrder, i)
			}
		}
		sort.Slice(index.sourceOrder, func(i, j int) bool {
			return pathPrefix(repositories[index.sourceOrder[i]].Root) < pathPrefix(repositories[index.sourceOrder[j]].Root)
		})
	})
	return &w.index
}

// RepositoryForPackage returns the source repository owning p.
func (w *Workspace) RepositoryForPackage(p *model.Package) *Repository {
	if w == nil || p == nil {
		return nil
	}
	if i, ok := w.indexed().byName[p.Repository]; ok {
		return &w.Repositories[i]
	}
	return nil
}

// RepositoryByName returns one participating repository by its stable
// .gitmodules identity (or the reserved control identity).
func (w *Workspace) RepositoryByName(name string) *Repository {
	if w == nil {
		return nil
	}
	if i, ok := w.indexed().byFold[globx.Fold(name)]; ok {
		return &w.Repositories[i]
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
	byRoot := w.indexed().byRoot
	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		if i, ok := byRoot[current]; ok {
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
	sourceOrder := w.indexed().sourceOrder
	prefix := pathPrefix(dir)
	i := sort.Search(len(sourceOrder), func(i int) bool {
		return pathPrefix(w.Repositories[sourceOrder[i]].Root) >= prefix
	})
	if i < len(sourceOrder) {
		repo := &w.Repositories[sourceOrder[i]]
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

func loadSubmodules(controlRoot string, overrides map[string]RepositoryOverrideConfig) ([]submodule, []DisabledRepository, error) {
	file := filepath.Join(controlRoot, ".gitmodules")
	if _, err := os.Stat(file); err != nil {
		if os.IsNotExist(err) {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: %s has no .gitmodules", controlRoot))
		}
		return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: read .gitmodules: %w", err))
	}
	out, err := gitOutput(controlRoot, "config", "--file", file, "--null", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: read .gitmodules: %w", err))
	}
	var modules []submodule
	var disabled []DisabledRepository
	seen := map[string]bool{}
	exactSeen := map[string]bool{}
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		key, path, ok := strings.Cut(record, "\n")
		if !ok {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: malformed git config output for .gitmodules"))
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")
		if name == "" || strings.EqualFold(name, ControlRepository) {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: invalid or reserved submodule name %q", name))
		}
		if seen[globx.Fold(name)] {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: duplicate submodule name %q (names are case-insensitive)", name))
		}
		seen[globx.Fold(name)] = true
		exactSeen[name] = true
		if override, ok := overrides[name]; ok && !override.IsEnabled() {
			// Disabled repositories stop at the read-only .gitmodules inventory.
			// In particular, do not require an initialized checkout or inspect its
			// history/pin: exclusion precedes every repository operation. The
			// declared boundary is retained so the paths stay reserved.
			if reserved, err := containedPath(controlRoot, path); err == nil {
				disabled = append(disabled, DisabledRepository{
					Name: name, Path: filepath.ToSlash(filepath.Clean(path)), Root: filepath.Clean(reserved)})
			}
			continue
		}
		abs, err := containedPath(controlRoot, path)
		if err != nil {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: submodule %q path %q: %w", name, path, err))
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: submodule %q is missing or uninitialized at %s", name, abs))
		}
		if resolved == controlRoot || !within(controlRoot, resolved) {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: submodule %q path %s resolves outside the control workspace", name, resolved))
		}
		gitRoot, err := gitOutput(resolved, "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: submodule %q is not initialized at %s", name, abs))
		}
		gitRoot, _ = filepath.EvalSymlinks(gitRoot)
		if gitRoot != resolved {
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: submodule %q path %s resolves to Git root %s", name, resolved, gitRoot))
		}
		modules = append(modules, submodule{Name: name, Path: filepath.ToSlash(filepath.Clean(path)), Root: resolved})
	}
	// A disabled repository leaves the module list above, so its override must
	// still be checked against the declared inventory here. An unknown name is
	// refused whether it was written to enable or to disable a repository: a
	// misspelled exclusion would otherwise release the repository it was meant
	// to hold back.
	for name := range overrides {
		if !exactSeen[name] {
			return nil, nil, WithDiagnostic(DiagnosticComposition, fmt.Errorf("polyrepo: repositoryOverrides names unknown source repository %q", name))
		}
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
			return nil, nil, WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: submodule roots overlap between %q (%s) and %q (%s)",
				byRoot[i-1].Name, byRoot[i-1].Root, byRoot[i].Name, byRoot[i].Root))
		}
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Name < modules[j].Name })
	sort.Slice(disabled, func(i, j int) bool { return disabled[i].Name < disabled[j].Name })
	return modules, disabled, nil
}

func requireCompleteRepository(root, name string) error {
	shallow, err := gitOutput(root, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: repository %q at %s is not initialized: %w", name, root, err))
	}
	switch shallow {
	case "true":
		return WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: repository %q at %s is shallow; complete history is required", name, root))
	case "false":
		return nil
	default:
		return WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: repository %q at %s returned a malformed completeness reply", name, root))
	}
}

func requirePinnedModule(controlRoot, controlRevision string, module submodule, runPins []string) (string, error) {
	pinned, head, err := readPinnedModule(controlRoot, controlRevision, module)
	if err != nil {
		return "", err
	}
	if !isAdmittedHead(head, pinned, runPins) {
		return "", refuseUnpinnedModule(module, head, pinned)
	}
	return head, nil
}

// readPinnedModule answers the revision control pins a source at and the
// revision the source's checkout is at.
func readPinnedModule(controlRoot, controlRevision string, module submodule) (pinned, head string, err error) {
	pinned, err = gitOutput(controlRoot, "rev-parse", controlRevision+":"+module.Path)
	if err != nil {
		return "", "", WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: source repository %q is not pinned by control HEAD: %w", module.Name, err))
	}
	head, err = gitOutput(module.Root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("polyrepo: source repository %q has no HEAD: %w", module.Name, err))
	}
	return pinned, head, nil
}

// isAdmittedHead reports whether a source checkout may take part: it is at the
// revision control pins, or at one the enclosing run admitted.
func isAdmittedHead(head, pinned string, runPins []string) bool {
	return head == pinned || slices.Contains(runPins, head)
}

func refuseUnpinnedModule(module submodule, head, pinned string) error {
	return WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("E330: polyrepo source repository %q is checked out at %s but control HEAD pins %s", module.Name, head, pinned))
}

// livePinPolicy is how a nested command reads a live run pin: how many times
// at most, and how long it waits between two reads.
type livePinPolicy struct {
	attempts int
	interval time.Duration
}

// livePinReads is the policy composition uses: ten reads a quarter of a second
// apart. The enclosing release publishes a revision a few milliseconds after
// it commits it, so the bound is what a checkout nobody admitted costs before
// its refusal rather than what an admitted one waits.
var livePinReads = livePinPolicy{attempts: 10, interval: 250 * time.Millisecond}

// livePinCheck is one source checkout validated against control HEAD and the
// run-scoped pins of an enclosing release.
type livePinCheck struct {
	controlRoot     string
	controlRevision string
	module          submodule
	runPins         []string
	resolve         SourcePinResolver
}

// requirePinnedModuleResolved validates one source checkout against control
// HEAD, admitting the run-scoped pins a resolver reports.
//
// The enclosing release commits into a source and only then publishes the
// revision it admitted, and nothing makes the two steps one transaction for a
// reader in another process: a nested command reading between them finds a
// HEAD nobody has admitted yet. So the reader proves its own observation
// instead (requireStableLivePin), and it reads again, a bounded number of
// times, before the checkout is refused with E330. The waits are derived from
// ctx, so an interrupt stops them at once.
func requirePinnedModuleResolved(ctx context.Context, controlRoot, controlRevision string, module submodule, runPins []string, resolve SourcePinResolver) (string, error) {
	if resolve == nil {
		return requirePinnedModule(controlRoot, controlRevision, module, runPins)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return requireStableLivePin(ctx, livePinCheck{
		controlRoot: controlRoot, controlRevision: controlRevision,
		module: module, runPins: runPins, resolve: resolve,
	}, livePinReads)
}

// requireStableLivePin reads the live pin, then HEAD, then the live pin again,
// and accepts HEAD when it is the revision control pins, or when the two pin
// reads agree and admit it. Two reads that disagree are the enclosing release
// publishing a revision while HEAD was read, and a HEAD they do not admit is
// that release between its commit and its pin: either is read again after
// policy.interval, at most policy.attempts times, and the last refusal is the
// one reported. A pin that cannot be read, and a checkout Git cannot answer
// for, are refused at once.
func requireStableLivePin(ctx context.Context, check livePinCheck, policy livePinPolicy) (string, error) {
	for attempt := 1; ; attempt++ {
		before, err := check.readLivePin()
		if err != nil {
			return "", err
		}
		pinned, head, err := readPinnedModule(check.controlRoot, check.controlRevision, check.module)
		if err != nil {
			return "", err
		}
		after, err := check.readLivePin()
		if err != nil {
			return "", err
		}
		// A checkout at control's own pin needs nothing from the run, so the
		// live pin moving around it says nothing about it either.
		isStable := slices.Equal(before, after)
		isAdmitted := isAdmittedHead(head, pinned, append(slices.Clone(check.runPins), before...))
		if head == pinned || (isStable && isAdmitted) {
			return head, nil
		}
		refusal := refuseUnpinnedModule(check.module, head, pinned)
		if isAdmitted {
			refusal = WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf(
				"E330: polyrepo source repository %q: its live run pin kept moving while the checkout was read", check.module.Name))
		}
		if attempt >= policy.attempts {
			return "", refusal
		}
		if err := waitForLivePin(ctx, policy.interval); err != nil {
			return "", WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf(
				"E330: polyrepo source repository %q: waiting for a stable live run pin: %w", check.module.Name, err))
		}
	}
}

// readLivePin is the enclosing release's current pin for the checked source.
func (c livePinCheck) readLivePin() ([]string, error) {
	pins, err := c.resolve(c.module.Name)
	if err != nil {
		return nil, WithDiagnostic(DiagnosticRepositoryInvalid,
			fmt.Errorf("E330: polyrepo source repository %q: reading live run pin: %w", c.module.Name, err))
	}
	return pins, nil
}

// waitForLivePin sleeps one interval between two reads, or less when ctx ends
// first, which is what lets Ctrl-C stop a nested command in composition.
func waitForLivePin(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// baselineOrigin is where one explicit baseline was written: the repository
// whose configuration states it and its index in that file's list.
type baselineOrigin struct {
	repository string
	index      int
}

// baselineResolution is what resolving the explicit baselines reads: the
// composed repositories a tuple may name, the repositories the run excluded,
// and, in a linked fleet, the origin of every tuple the peers' files merged
// into one list. With no origins every tuple comes from one file.
type baselineResolution struct {
	repos        []Repository
	participants *participation
	origins      []baselineOrigin
}

// origin answers where tuple i was written. Without recorded origins every
// tuple is the one configuration file's own, at its own index.
func (r baselineResolution) origin(i int) baselineOrigin {
	if i < len(r.origins) {
		return r.origins[i]
	}
	return baselineOrigin{index: i}
}

// resolveRepositoryBaselines validates every explicit baseline and resolves
// its revision to a full commit in the repository it names (CCME §27.6).
//
// Two tuples sharing a (consumer, releaseTag, repository) key are E333 when
// one file states both. In a linked fleet two peers may state the same
// boundary (§27.11): tuples that resolve to one commit are that boundary, kept
// once, and tuples that resolve to different commits are E333 naming both
// peers and both revisions, so the plan never depends on which peer the run
// was started from.
func resolveRepositoryBaselines(cfg *File, resolution baselineResolution) error {
	byName := make(map[string]Repository, len(resolution.repos))
	for _, repo := range resolution.repos {
		byName[repo.Name] = repo
	}
	type stated struct {
		baseline RepositoryBaselineConfig
		origin   baselineOrigin
		written  string
	}
	firstByKey := map[string]stated{}
	statedIn := map[string]RepositoryBaselineConfig{}
	kept := make([]RepositoryBaselineConfig, 0, len(cfg.RepositoryBaselines))
	for i := range cfg.RepositoryBaselines {
		b := cfg.RepositoryBaselines[i]
		origin := resolution.origin(i)
		where := describeBaselineOrigin(origin)
		if strings.TrimSpace(b.Consumer) == "" || strings.TrimSpace(b.ReleaseTag) == "" ||
			strings.TrimSpace(b.Repository) == "" || strings.TrimSpace(b.Revision) == "" {
			return WithDiagnostic(DiagnosticBoundary, fmt.Errorf("config: %s: consumer, releaseTag, repository and revision are required", where))
		}
		key := globx.Fold(b.Consumer) + "\x00" + b.ReleaseTag + "\x00" + b.Repository
		if previous, ok := statedIn[origin.repository+"\x00"+key]; ok {
			return WithDiagnostic(DiagnosticBoundary, fmt.Errorf("config: %s duplicates baseline for consumer %q and releaseTag %q (previous repository %q revision %q)",
				where, b.Consumer, b.ReleaseTag, previous.Repository, previous.Revision))
		}
		statedIn[origin.repository+"\x00"+key] = b
		written := b.Revision
		if err := resolution.resolveBaseline(&b, where, byName); err != nil {
			return err
		}
		first, isStated := firstByKey[key]
		if !isStated {
			firstByKey[key] = stated{baseline: b, origin: origin, written: written}
			kept = append(kept, b)
			continue
		}
		if first.baseline.Revision != b.Revision {
			return WithDiagnostic(DiagnosticBoundary, fmt.Errorf(
				"config: repositories %q and %q state conflicting baselines for consumer %q, releaseTag %q and repository %q: "+
					"revision %q (commit %s) and revision %q (commit %s); state one boundary",
				first.origin.repository, origin.repository, b.Consumer, b.ReleaseTag, b.Repository,
				first.written, first.baseline.Revision, written, b.Revision))
		}
	}
	cfg.RepositoryBaselines = kept
	return nil
}

// describeBaselineOrigin names where a tuple was written for a refusal: its
// index, and in a linked fleet the peer whose file states it.
func describeBaselineOrigin(origin baselineOrigin) string {
	if origin.repository == "" {
		return fmt.Sprintf("repositoryBaselines[%d]", origin.index)
	}
	return fmt.Sprintf("repository %q repositoryBaselines[%d]", origin.repository, origin.index)
}

// resolveBaseline checks that one tuple names a participating repository and
// a commit reachable from its head, and rewrites the tuple to that
// repository's exact name and the commit's full object ID.
func (r baselineResolution) resolveBaseline(b *RepositoryBaselineConfig, where string, byName map[string]Repository) error {
	repo, ok := byName[b.Repository]
	if !ok {
		for _, excluded := range r.participants.disabled {
			if strings.EqualFold(excluded.Name, b.Repository) {
				return WithDiagnostic(DiagnosticBoundary, fmt.Errorf(
					"config: %s: repository %q is excluded by repositoryOverrides; an excluded repository supplies no baseline",
					where, b.Repository))
			}
		}
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
	return nil
}

func DiscoverWorkspace(c *File, controlRoot string, workspace *Workspace) ([]*model.Package, []model.Dependency, []ExcludedDir, error) {
	pkgs, active, _, excluded, err := DiscoverWorkspacePlan(c, controlRoot, workspace)
	return pkgs, active, excluded, err
}

// ResolvedWorkspaceSpaceConfigs resolves a composed workspace's space-only
// folder layers with the same ownership policy as DiscoverWorkspace. The slice
// retains equal local space names from different imported repositories
// because either may define the only occurrence of a run script. A single
// history resolves its own with ResolvedSpaceConfigs.
func ResolvedWorkspaceSpaceConfigs(workspace *Workspace) ([]SpaceConfig, error) {
	gitRoots := &gitRootMemo{byDir: make(map[string]string)}
	var out []SpaceConfig
	for i := range workspace.Repositories {
		repository := &workspace.Repositories[i]
		if !repository.Control && !repository.Imported {
			continue
		}
		folderInputs := newWorkspaceFolderPolicy(workspace, repository, gitRoots)
		resolved, err := resolvedSpaceConfigsMode(repository.Config, repository.Root, folderInputs.allow)
		if err != nil {
			return nil, err
		}
		for _, name := range sortedSpaceNames(repository.Config) {
			out = append(out, resolved[name])
		}
	}
	return out, nil
}

// ResolvedRepositorySpaceConfigs settles one owner's spaces while refusing
// folder configuration belonging to another participating repository.
func ResolvedRepositorySpaceConfigs(c *File, root string, workspace *Workspace) (map[string]SpaceConfig, error) {
	if workspace == nil {
		return ResolvedSpaceConfigs(c, root)
	}
	for i := range workspace.Repositories {
		repository := &workspace.Repositories[i]
		if repository.Root != root {
			continue
		}
		gitRoots := &gitRootMemo{byDir: make(map[string]string)}
		folderInputs := newWorkspaceFolderPolicy(workspace, repository, gitRoots)
		return resolvedSpaceConfigsMode(c, root, folderInputs.allow)
	}
	return nil, fmt.Errorf("space configuration has no owner for %s", root)
}

// DiscoverWorkspacePlan returns both active dependency edges and declared
// external edges whose provider is absent from this snapshot. Planning keeps
// the latter out of the graph but reports their inactive state explicitly.
func DiscoverWorkspacePlan(c *File, controlRoot string, workspace *Workspace) ([]*model.Package, []model.Dependency, []model.Dependency, []ExcludedDir, error) {
	pkgs, declared, excluded, err := DiscoverWorkspacePackages(c, controlRoot, workspace)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	active, inactive, err := validateDependenciesForPlan(pkgs, declared, workspace.unknownPackageRemedy)
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
		folderInputs := newWorkspaceFolderPolicy(workspace, &repo, gitRoots)
		local, deps, ex, err = discoverPackagesMode(repo.Config, repo.Root, folderInputs.allow)
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
				if p.Space.Versioning.IsShared() {
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
	if err := validatePackageOwnershipMode(pkgs, workspace.IsLinked()); err != nil {
		return nil, nil, nil, err
	}
	// The entry's sweep roots, and only the entry's: an imported repository's
	// own are validated where its file is read and never consulted (§28.10).
	if err := checkRunOutputRoots(c.RunOutputs, pkgs, controlRoot); err != nil {
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

// workspaceFolderPolicy admits implicit configuration and ignore files only
// when their canonical folder belongs to the repository whose explicit config
// is being resolved. The nearest-Git-root check keeps an unlisted nested
// worktree from speaking before package ownership can reject it with E331.
type workspaceFolderPolicy struct {
	workspace      *Workspace
	repositoryName string
	repositoryRoot string
	gitRoots       *gitRootMemo
	byPath         map[string]bool
	byCanonical    map[string]bool
}

func newWorkspaceFolderPolicy(workspace *Workspace, repository *Repository, gitRoots *gitRootMemo) *workspaceFolderPolicy {
	return &workspaceFolderPolicy{
		workspace:      workspace,
		repositoryName: repository.Name,
		repositoryRoot: repository.Root,
		gitRoots:       gitRoots,
		byPath:         make(map[string]bool),
		byCanonical:    make(map[string]bool),
	}
}

func (p *workspaceFolderPolicy) allow(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	if allowed, ok := p.byPath[abs]; ok {
		return allowed
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		p.byPath[abs] = false
		return false
	}
	canonical = filepath.Clean(canonical)
	if allowed, ok := p.byCanonical[canonical]; ok {
		p.byPath[abs] = allowed
		return allowed
	}
	owner := p.workspace.repositoryForCanonicalDir(canonical)
	allowed := owner != nil && owner.Name == p.repositoryName && owner.Root == p.repositoryRoot
	if allowed {
		actual, rootErr := p.gitRoots.nearest(canonical)
		allowed = rootErr == nil && actual == p.repositoryRoot
	}
	p.byPath[abs] = allowed
	p.byCanonical[canonical] = allowed
	return allowed
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

// validatePackageOwnershipMode scopes containment to one repository at a time
// in a linked fleet.
//
// Package names stay one graph either way: two repositories may not both
// declare `api`, whichever topology composed them. What differs is containment. A
// central fleet is one tree of folders with every source inside the
// control repository, so a scope containing another package's scope is always
// an ownership mistake. A linked peer sits inside the checkout of the
// repository that links it, so a repository whose own package covers its root
// contains every peer linked beneath it, and comparing those scopes across
// repositories would report an overlap that ownership does not have: the
// deepest Git root already owns each of those folders, and the per-package
// checks above have already refused any package that crosses that boundary.
func validatePackageOwnershipMode(pkgs []*model.Package, perRepository bool) error {
	byName := map[string]*model.Package{}
	for _, p := range pkgs {
		fold := globx.Fold(p.Name)
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
		if perRepository {
			key = p.Repository + "\x00" + key
		}
		scopes[i] = scope{pkg: p, key: key}
	}
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].key < scopes[j].key })
	for i := 1; i < len(scopes); i++ {
		if perRepository && scopes[i].pkg.Repository != scopes[i-1].pkg.Repository {
			continue
		}
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
	return gitOutputContext(context.Background(), dir, args...)
}

// gitOutputContext is gitOutput under the caller's context. Composition of a
// linked fleet runs several Git processes per repository, and an operator who
// interrupts a command queued behind them must stop at the current one rather
// than at the end of the walk.
func gitOutputContext(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", &gitError{text: fmt.Sprintf("git %s: %s", strings.Join(args, " "), message), err: err}
	}
	return strings.TrimSpace(string(out)), nil
}

// gitError is a failed Git invocation in the words this package has always
// reported it in, with the process failure still reachable underneath. The
// text is what a reader sees; the wrapped error is how a caller tells "the
// key you asked about is not there" (status 1) from "this file cannot be read
// at all" without parsing the message.
type gitError struct {
	text string
	err  error
}

func (e *gitError) Error() string { return e.text }
func (e *gitError) Unwrap() error { return e.err }
