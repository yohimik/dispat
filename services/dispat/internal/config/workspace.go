package config

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

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
	if cfg == nil || (!cfg.Polyrepo && len(cfg.Configs) == 0 && len(cliConfigs) == 0) {
		return nil, nil
	}
	cfg.Polyrepo = true
	root, err := filepath.Abs(controlRoot)
	if err != nil {
		return nil, fmt.Errorf("polyrepo: resolve control root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("polyrepo: resolve control root: %w", err)
	}
	if err := requireCompleteRepository(root, ControlRepository); err != nil {
		return nil, err
	}
	modules, err := loadSubmodules(root)
	if err != nil {
		return nil, err
	}
	repos := []Repository{{Name: ControlRepository, Root: root, ConfigPath: configPath, Config: cfg, Control: true, Commit: cfg.Commit}}

	seenConfig := map[string]bool{}
	if canonical, err := canonicalFile(configPath); err == nil {
		seenConfig[canonical] = true
	}
	importedRepo := map[string]string{}
	imports, err := workspaceImports(cfg, configPath, root, cliConfigs)
	if err != nil {
		return nil, err
	}
	for _, imp := range imports {
		declared := imp.Path
		path, err := resolveImportPath(root, imp.Base, declared)
		if err != nil {
			return nil, fmt.Errorf("polyrepo: config %q: %w", declared, err)
		}
		canonical, err := canonicalFile(path)
		if err != nil {
			return nil, fmt.Errorf("polyrepo: config %q: %w", declared, err)
		}
		if seenConfig[canonical] {
			continue
		}
		seenConfig[canonical] = true
		repoRoot, err := gitOutput(filepath.Dir(canonical), "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, fmt.Errorf("polyrepo: imported config %s is not inside an initialized Git repository: %w", canonical, err)
		}
		repoRoot, err = filepath.EvalSymlinks(repoRoot)
		if err != nil {
			return nil, fmt.Errorf("polyrepo: imported repository root %s: %w", repoRoot, err)
		}
		name, ok := moduleNameByRoot(modules, repoRoot)
		if !ok {
			return nil, fmt.Errorf("polyrepo: imported config %s belongs to %s, which is not an initialized .gitmodules repository", canonical, repoRoot)
		}
		if previous := importedRepo[name]; previous != "" && previous != canonical {
			return nil, fmt.Errorf("polyrepo: repository %q has conflicting imported configs %s and %s", name, previous, canonical)
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
			return nil, fmt.Errorf("polyrepo: imported config %s declares configs; nested workspace imports are not allowed", canonical)
		}
		repos = append(repos, Repository{Name: name, Root: repoRoot, ConfigPath: canonical, Config: imported, Imported: true, Commit: imported.Commit})
	}
	for key := range cfg.RepositoryOverrides {
		module, ok := moduleByName(modules, key)
		if !ok {
			return nil, fmt.Errorf("polyrepo: repositoryOverrides names unknown source repository %q", key)
		}
		if importedRepo[module.Name] != "" {
			return nil, fmt.Errorf("polyrepo: repositoryOverrides[%q] cannot override imported repository config %s", key, importedRepo[module.Name])
		}
	}
	for _, module := range modules {
		if _, imported := importedRepo[module.Name]; imported {
			continue
		}
		commit := cfg.Commit
		if key, override, ok := foldRepositoryOverride(cfg.RepositoryOverrides, module.Name); ok && override.Commit != nil {
			_ = key
			commit = override.Commit
		}
		repos = append(repos, Repository{Name: module.Name, Root: module.Root, Config: cfg, Commit: commit})
	}

	// Only initialized, pinned repositories may participate. Check all source
	// roots once here; package discovery below merely assigns packages to them.
	for _, module := range modules {
		if err := requireCompleteRepository(module.Root, module.Name); err != nil {
			return nil, err
		}
		if err := requirePinnedModule(root, module); err != nil {
			return nil, err
		}
	}
	if err := resolveRepositoryBaselines(cfg, repos); err != nil {
		return nil, err
	}
	return &Workspace{ControlRoot: root, Repositories: repos, modules: modules}, nil
}

// Repository is one participant of a composed workspace.
type Repository struct {
	Name       string
	Root       string
	ConfigPath string
	Config     *File
	Control    bool
	Imported   bool
	Commit     *CommitConfig
}

// Workspace is the repository ownership map shared by every command in one
// run. It is immutable after composition.
type Workspace struct {
	ControlRoot  string
	Repositories []Repository
	modules      []submodule
}

// RepositoryForPackage returns the source repository owning p.
func (w *Workspace) RepositoryForPackage(p *model.Package) *Repository {
	if w == nil || p == nil {
		return nil
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
	var best *Repository
	for i := range w.Repositories {
		repo := &w.Repositories[i]
		if within(repo.Root, dir) && (best == nil || len(repo.Root) > len(best.Root)) {
			best = repo
		}
	}
	return best
}

func foldRepositoryOverride(overrides map[string]RepositoryOverrideConfig, name string) (string, RepositoryOverrideConfig, bool) {
	for key, override := range overrides {
		if strings.EqualFold(key, name) {
			return key, override, true
		}
	}
	return "", RepositoryOverrideConfig{}, false
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
			return nil, fmt.Errorf("polyrepo: %s has no .gitmodules", controlRoot)
		}
		return nil, fmt.Errorf("polyrepo: read .gitmodules: %w", err)
	}
	out, err := gitOutput(controlRoot, "config", "--file", file, "--null", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		return nil, fmt.Errorf("polyrepo: read .gitmodules: %w", err)
	}
	var modules []submodule
	seen := map[string]bool{}
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		key, _, ok := strings.Cut(record, "\n")
		if !ok {
			return nil, fmt.Errorf("polyrepo: malformed git config output for .gitmodules")
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")
		if name == "" || strings.EqualFold(name, ControlRepository) {
			return nil, fmt.Errorf("polyrepo: invalid or reserved submodule name %q", name)
		}
		if seen[strings.ToLower(name)] {
			return nil, fmt.Errorf("polyrepo: duplicate submodule name %q (names are case-insensitive)", name)
		}
		seen[strings.ToLower(name)] = true
		path, err := gitOutput(controlRoot, "config", "--file", file, "--get", key)
		if err != nil {
			return nil, fmt.Errorf("polyrepo: read path for submodule %q: %w", name, err)
		}
		abs, err := containedPath(controlRoot, path)
		if err != nil {
			return nil, fmt.Errorf("polyrepo: submodule %q path %q: %w", name, path, err)
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("polyrepo: submodule %q is missing or uninitialized at %s", name, abs)
		}
		gitRoot, err := gitOutput(resolved, "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, fmt.Errorf("polyrepo: submodule %q is not initialized at %s", name, abs)
		}
		gitRoot, _ = filepath.EvalSymlinks(gitRoot)
		if gitRoot != resolved {
			return nil, fmt.Errorf("polyrepo: submodule %q path %s resolves to Git root %s", name, resolved, gitRoot)
		}
		modules = append(modules, submodule{Name: name, Path: filepath.ToSlash(path), Root: resolved})
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Name < modules[j].Name })
	return modules, nil
}

func requireCompleteRepository(root, name string) error {
	shallow, err := gitOutput(root, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return fmt.Errorf("polyrepo: repository %q at %s is not initialized: %w", name, root, err)
	}
	if shallow == "true" {
		return fmt.Errorf("polyrepo: repository %q at %s is shallow; complete history is required", name, root)
	}
	return nil
}

func requirePinnedModule(controlRoot string, module submodule) error {
	pinned, err := gitOutput(controlRoot, "rev-parse", "HEAD:"+module.Path)
	if err != nil {
		return fmt.Errorf("polyrepo: source repository %q is not pinned by control HEAD: %w", module.Name, err)
	}
	head, err := gitOutput(module.Root, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("polyrepo: source repository %q has no HEAD: %w", module.Name, err)
	}
	if pinned != head {
		return fmt.Errorf("E330: polyrepo source repository %q is checked out at %s but control HEAD pins %s", module.Name, head, pinned)
	}
	return nil
}

func resolveRepositoryBaselines(cfg *File, repos []Repository) error {
	byName := make(map[string]Repository, len(repos))
	for _, repo := range repos {
		byName[strings.ToLower(repo.Name)] = repo
	}
	seen := map[string]RepositoryBaselineConfig{}
	for i := range cfg.RepositoryBaselines {
		b := &cfg.RepositoryBaselines[i]
		where := fmt.Sprintf("repositoryBaselines[%d]", i)
		if strings.TrimSpace(b.Consumer) == "" || strings.TrimSpace(b.ReleaseTag) == "" ||
			strings.TrimSpace(b.Repository) == "" || strings.TrimSpace(b.Revision) == "" {
			return fmt.Errorf("config: %s: consumer, releaseTag, repository and revision are required", where)
		}
		key := strings.ToLower(b.Consumer) + "\x00" + b.ReleaseTag + "\x00" + strings.ToLower(b.Repository)
		if previous, ok := seen[key]; ok {
			return fmt.Errorf("config: %s duplicates baseline for consumer %q and releaseTag %q (previous repository %q revision %q)",
				where, b.Consumer, b.ReleaseTag, previous.Repository, previous.Revision)
		}
		repo, ok := byName[strings.ToLower(b.Repository)]
		if !ok {
			return fmt.Errorf("config: %s: unknown repository %q", where, b.Repository)
		}
		oid, err := gitOutput(repo.Root, "rev-parse", "--verify", b.Revision+"^{commit}")
		if err != nil {
			return fmt.Errorf("config: %s: revision %q is not a commit in repository %q", where, b.Revision, repo.Name)
		}
		if _, err := gitOutput(repo.Root, "merge-base", "--is-ancestor", oid, "HEAD"); err != nil {
			return fmt.Errorf("config: %s: revision %q is not reachable from repository %q HEAD", where, b.Revision, repo.Name)
		}
		b.Repository = repo.Name
		b.Revision = oid
		seen[key] = *b
	}
	return nil
}

func DiscoverWorkspace(c *File, controlRoot string, workspace *Workspace) ([]*model.Package, []model.Dependency, []ExcludedDir, error) {
	pkgs, declared, excluded, err := DiscoverWorkspacePackages(c, controlRoot, workspace)
	if err != nil {
		return nil, nil, nil, err
	}
	active, err := validateDependencies(pkgs, declared)
	if err != nil {
		return nil, nil, nil, err
	}
	return pkgs, active, excluded, nil
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
				owner, ownerRoot, err = packageModule(p, workspace.ControlRoot, workspace.modules)
				if err != nil {
					return nil, nil, nil, err
				}
			} else if !within(repo.Root, p.Dir) || !within(repo.Root, p.ScopeDir()) {
				return nil, nil, nil, fmt.Errorf("polyrepo: imported repository %q package %q path %s escapes its owner root %s", repo.Name, p.Name, p.Dir, repo.Root)
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

func packageModule(p *model.Package, controlRoot string, modules []submodule) (string, string, error) {
	dir, err := filepath.EvalSymlinks(p.Dir)
	if err != nil {
		return "", "", fmt.Errorf("polyrepo: package %q: %w", p.Name, err)
	}
	for _, module := range modules {
		if within(module.Root, dir) {
			return module.Name, module.Root, nil
		}
	}
	if !within(controlRoot, dir) {
		return "", "", fmt.Errorf("polyrepo: package %q path %s escapes the control repository", p.Name, p.Dir)
	}
	// A control-owned package cannot wrap a source checkout. Its file scope
	// would otherwise cross histories, and an ordinary control commit could
	// ambiguously claim files whose commit object belongs to a source.
	for _, module := range modules {
		if within(dir, module.Root) {
			return "", "", fmt.Errorf("polyrepo: package %q path %s spans source repository %q at %s", p.Name, p.Dir, module.Name, module.Root)
		}
	}
	return ControlRepository, controlRoot, nil
}

func validatePackageOwnership(pkgs []*model.Package) error {
	byName := map[string]*model.Package{}
	for _, p := range pkgs {
		fold := strings.ToLower(p.Name)
		if previous := byName[fold]; previous != nil {
			return fmt.Errorf("polyrepo: duplicate package name %q in repositories %q and %q (names are case-insensitive)", p.Name, previous.Repository, p.Repository)
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
		key := strings.TrimRight(filepath.Clean(p.ScopeDir()), separator) + separator
		scopes[i] = scope{pkg: p, key: key}
	}
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].key < scopes[j].key })
	for i := 1; i < len(scopes); i++ {
		if strings.HasPrefix(scopes[i].key, scopes[i-1].key) {
			a, b := scopes[i-1].pkg, scopes[i].pkg
			return fmt.Errorf("polyrepo: package ownership overlaps between %q (%s) and %q (%s)", a.Name, a.ScopeDir(), b.Name, b.ScopeDir())
		}
	}
	return nil
}

func moduleNameByRoot(modules []submodule, root string) (string, bool) {
	for _, module := range modules {
		if module.Root == root {
			return module.Name, true
		}
	}
	return "", false
}

func moduleByName(modules []submodule, name string) (submodule, bool) {
	for _, module := range modules {
		if strings.EqualFold(module.Name, name) {
			return module, true
		}
	}
	return submodule{}, false
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
	declaringFile, _, err := ResolveEdit(configPath, []string{"configs"})
	if err != nil {
		return nil, fmt.Errorf("polyrepo: resolve configs declaration: %w", err)
	}
	base := filepath.Dir(declaringFile)
	var out []workspaceImport
	for _, item := range cfg.Configs {
		out = append(out, workspaceImport{Path: item, Base: base})
	}
	for _, item := range cli {
		out = append(out, workspaceImport{Path: item, Base: controlRoot})
	}
	return out, nil
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
