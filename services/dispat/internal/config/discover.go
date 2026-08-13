package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/ignore"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// discovery is one DiscoverPackages call: the accumulators every phase of it
// writes into, so the phases can be read one at a time instead of as one
// sequence holding a dozen live variables.
//
// The order the phases run in is the order the configuration layers apply in,
// and it is load-bearing rather than incidental: the spaces first, because a
// folder is what makes a package exist; then the standalone entries, which
// may only take names no folder claimed; then the checks that need every
// package to be known, which is why they cannot happen as each one is built.
type discovery struct {
	c    *File
	root string

	declared []DeclaredDependency
	pkgs     []*model.Package
	// owner maps a package name onto the space that holds it, "" for a
	// standalone one. It doubles as the uniqueness check and as the set every
	// name reference is resolved against once discovery is done.
	owner      map[string]string
	consumed   map[string][]string // top-level packages key -> matching folders
	excluded   []excludedDir
	baseIgnore ignore.Chain

	// spaceConfigs and onlyChecks feed the autoVersion.only check, which needs
	// every package discovered before it can say a name is unknown.
	spaceConfigs map[string]SpaceConfig
	onlyChecks   []onlyCheck
}

// onlyCheck is one autoVersion block whose `only` list is still to be held
// against the discovered packages, with the label its error should carry.
type onlyCheck struct {
	label string
	av    *AutoVersionConfig
}

// spaceScan is one space's resolved configuration together with what its
// folder walk accumulates. Everything on it is derived once and shared by
// every package of the space that adds no layer of its own.
type spaceScan struct {
	name string
	dir  string
	sc   SpaceConfig
	file SpaceFile
	src  string // where the space folder's own config file was read from

	base      *model.Space
	baseScope scriptScope
	// baseRefsChecked records that the space's own references have been
	// resolved: every package without an override layer shares the space's
	// scope, so its references are the same question with the same answer.
	baseRefsChecked bool

	chain                      ignore.Chain
	changelog                  model.ChangelogSpec
	github                     model.GitHubSpec
	buildWeight, publishWeight int

	excluded []excludedDir
	// The space's two package-level maps account for their keys against the
	// folders of this space alone.
	spaceConsumed map[string][]string
	fileConsumed  map[string][]string
}

// newDiscovery resolves what every space starts from: the repository's own
// dependency declarations and its ignore layer.
func newDiscovery(c *File, root string) (*discovery, error) {
	rootIgnore, err := ignoreLayer(root, c.Ignore)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", DispatignoreName, err)
	}
	return &discovery{
		c:    c,
		root: root,
		// The merged declaration list: the root config's own `dependencies`
		// first, in file order, then each space's object and each package's
		// list in discovery order.
		declared:     collectObjectDeps(nil, c.Dependencies, DepSource{KeyPath: []string{"dependencies"}}),
		owner:        make(map[string]string),
		consumed:     make(map[string][]string),
		baseIgnore:   appendLayer(nil, rootIgnore),
		spaceConfigs: make(map[string]SpaceConfig, len(c.Spaces)),
	}, nil
}

// resolveSpace settles one space's configuration: the root file's defaults,
// then the space entry over them, then the space folder's own config file,
// which is the layer between the entry and anything said about one package.
func (d *discovery) resolveSpace(sn string) (*spaceScan, error) {
	sc := spaceBase(d.c, d.c.Spaces[sn])
	dir := filepath.Join(d.root, sc.Path)

	// A space rooted at the repository itself has no space-file layer: the
	// file in that folder is the root config, and merging it into itself
	// would be reading one statement twice.
	var spaceFile SpaceFile
	var spaceSrc string
	if !sameDir(dir, d.root) {
		var err error
		if spaceFile, spaceSrc, err = loadSpaceFile(dir); err != nil {
			return nil, fmt.Errorf("config: space %q: %w", sn, err)
		}
	}
	if spaceSrc != "" {
		label := fmt.Sprintf("space %q (%s)", sn, spaceSrc)
		sc = mergePackageOverride(sc, spaceOverride(spaceFile))
		var err error
		if sc, err = validateSpaceAs(label, sc); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		if err := validateSpacePackages(label+": packages", spaceFile.Packages); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}
	d.spaceConfigs[sn] = sc

	if err := d.collectSpaceDeps(sn, spaceFile, spaceSrc); err != nil {
		return nil, err
	}

	// The space's level speaks once, whether it was written in the root
	// file's space entry, in the space folder's own config file, or in a
	// .dispatignore next to them.
	spacePatterns := append(append([]string{}, d.c.Spaces[sn].Ignore...), spaceFile.Ignore...)
	spaceIgnore, err := ignoreLayer(dir, spacePatterns)
	if err != nil {
		return nil, fmt.Errorf("config: space %q: %w", sn, err)
	}
	buildWeight, publishWeight := packageWeights(sc.Concurrency)
	baseScope := packageScope(d.c, sc)
	base, err := buildSpace(d.c, baseScope, fmt.Sprintf("space %q", sn), sn, dir, sc)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &spaceScan{
		name: sn, dir: dir, sc: sc, file: spaceFile, src: spaceSrc,
		base: base, baseScope: baseScope,
		chain:         appendLayer(d.baseIgnore, spaceIgnore),
		changelog:     changelogSpec(sc.Changelog),
		github:        githubSpec(sc.GitHub),
		buildWeight:   buildWeight,
		publishWeight: publishWeight,
		spaceConsumed: make(map[string][]string),
		fileConsumed:  make(map[string][]string),
	}, nil
}

// collectSpaceDeps takes in the space's own edges, before its packages': the
// root file's space entry first, then the space folder's file, each labelled
// with where it was written. Whether they touch this space needs the packages
// of every space and is checked once discovery is done.
func (d *discovery) collectSpaceDeps(sn string, spaceFile SpaceFile, spaceSrc string) error {
	if err := validateObjectDeps(fmt.Sprintf("spaces[%q]: dependencies", sn), d.c.Spaces[sn].Dependencies); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	d.declared = collectObjectDeps(d.declared, d.c.Spaces[sn].Dependencies,
		DepSource{Space: sn, KeyPath: []string{"spaces", sn, "dependencies"}})
	if len(spaceFile.Dependencies) > 0 {
		if err := validateObjectDeps(spaceSrc+": dependencies", spaceFile.Dependencies); err != nil {
			return fmt.Errorf("config: space %q: %w", sn, err)
		}
		d.declared = collectObjectDeps(d.declared, spaceFile.Dependencies,
			DepSource{Space: sn, File: spaceSrc, KeyPath: []string{"dependencies"}})
	}
	return nil
}

// scanSpace walks one space's folders, building a package out of each one
// the space's .dispatexclude did not remove, and then holds the space's two
// package maps to the folders it found.
func (d *discovery) scanSpace(sn string) error {
	s, err := d.resolveSpace(sn)
	if err != nil {
		return err
	}
	exclude, err := loadExclude(s.dir)
	if err != nil {
		return fmt.Errorf("config: space %q: %s: %w", sn, DispatexcludeName, err)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("config: space %q: %w", sn, err)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		if excludedName(exclude, name) {
			s.excluded = append(s.excluded, excludedDir{sn, name})
			continue
		}
		if prev, dup := d.owner[name]; dup {
			return fmt.Errorf(
				"config: package %q exists in both space %q and space %q; package names must be unique",
				name, prev, sn)
		}
		d.owner[name] = sn
		pkg, err := d.spacePackage(s, name)
		if err != nil {
			return err
		}
		d.pkgs = append(d.pkgs, pkg)
	}
	if err := (keyCheck{
		label:    fmt.Sprintf("spaces[%q]: packages", sn),
		entries:  s.sc.Packages,
		consumed: s.spaceConsumed,
		ignored:  s.excluded,
		missing:  fmt.Sprintf("matches no folder of space %q (a package of another space is configured by that space, or by the top-level packages map)", sn),
	}).run(); err != nil {
		return err
	}
	if err := (keyCheck{
		label:    fmt.Sprintf("%s: packages", s.src),
		entries:  s.file.Packages,
		consumed: s.fileConsumed,
		ignored:  s.excluded,
		missing:  fmt.Sprintf("matches no folder of space %q", sn),
	}).run(); err != nil {
		return err
	}
	d.excluded = append(d.excluded, s.excluded...)
	return nil
}

// spacePackage builds one package of a space: the space's own answers when
// nothing overrides them, and a derived Space of its own when something does.
func (d *discovery) spacePackage(s *spaceScan, name string) (*model.Package, error) {
	pkg := &model.Package{
		Name:          name,
		Dir:           filepath.Join(s.dir, name),
		Space:         s.base,
		BuildWeight:   s.buildWeight,
		PublishWeight: s.publishWeight,
		Changelog:     s.changelog,
		GitHub:        s.github,
		Src:           s.sc.Src,
	}
	label := fmt.Sprintf("space %q: package %q", s.name, name)
	layers, err := d.packageLayers(s, name, pkg.Dir, label)
	if err != nil {
		return nil, err
	}
	if len(layers) == 0 {
		if pkg.Ignore, err = packageIgnore(s.chain, pkg.Dir, nil); err != nil {
			return nil, fmt.Errorf("config: %s: %w", label, err)
		}
		if !s.baseRefsChecked {
			// No override layer: the package resolves the space's own
			// references, but the error still names the package, because the
			// scope a reference has to resolve in is always a package's.
			if err := s.baseScope.checkSpaceRefs(label, s.sc); err != nil {
				return nil, fmt.Errorf("config: %w", err)
			}
			s.baseRefsChecked = true
		}
		return pkg, nil
	}
	merged, ex, autoVersioned, withDeps, err := applyLayers(d.c, s.sc, name, layers, d.declared)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	d.declared = withDeps
	if merged, err = validateSpaceAs(label, merged); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	scope := packageScope(d.c, merged)
	if err := scope.checkSpaceRefs(label, merged); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if pkg.Space, err = buildSpace(d.c, scope, label, s.name, s.dir, merged); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if autoVersioned {
		d.onlyChecks = append(d.onlyChecks, onlyCheck{label, merged.AutoVersion})
	}
	applyMerged(pkg, merged, ex)
	if pkg.Ignore, err = packageIgnore(s.chain, pkg.Dir, ex.ignore); err != nil {
		return nil, fmt.Errorf("config: %s: %w", label, err)
	}
	return pkg, nil
}

// packageLayers gathers the override layers that speak about one package of a
// space, nearest last: the top-level `packages` entry, the space's own
// `packages` entry, the space file's, and the file in the package folder.
func (d *discovery) packageLayers(s *spaceScan, name, dir, label string) ([]overrideLayer, error) {
	key := strings.ToLower(name)
	var layers []overrideLayer
	if entryPO, ok := d.c.Package(name); ok {
		if entryPO.Path != "" {
			return nil, fmt.Errorf(
				"config: packages[%q]: package %q belongs to space %q, its location is the space folder, so path cannot be set",
				key, name, s.name)
		}
		d.consumed[key] = append(d.consumed[key], name)
		layers = append(layers, overrideLayer{entryPO, label,
			DepSource{KeyPath: []string{"packages", key, "dependencies"}}})
	}
	if spacePO, ok := s.sc.Package(name); ok {
		s.spaceConsumed[key] = append(s.spaceConsumed[key], name)
		layers = append(layers, overrideLayer{spacePO, label + ": the space's packages entry",
			DepSource{KeyPath: []string{"spaces", s.name, "packages", key, "dependencies"}}})
	}
	if filePO, ok := s.file.Package(name); ok {
		s.fileConsumed[key] = append(s.fileConsumed[key], name)
		layers = append(layers, overrideLayer{filePO, fmt.Sprintf("%s (%s: packages entry)", label, s.src),
			DepSource{File: s.src, KeyPath: []string{"packages", key, "dependencies"}}})
	}
	folderPO, folderSrc, err := loadPackageFile(dir)
	if err != nil {
		return nil, fmt.Errorf("config: space %q: package %q: %w", s.name, name, err)
	}
	if folderSrc != "" {
		layers = append(layers, overrideLayer{folderPO, fmt.Sprintf("%s (%s)", label, folderSrc),
			DepSource{File: folderSrc, KeyPath: []string{"dependencies"}}})
	}
	return layers, nil
}

// scanStandalone builds the packages declared by a top-level `packages` entry
// with a path, in deterministic key order. An entry whose key matched a
// folder was rejected before this runs, so every name here is new.
func (d *discovery) scanStandalone() error {
	var keys []string
	for key, po := range d.c.Packages {
		if po.Path != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		pkg, err := d.standalonePackage(key)
		if err != nil {
			return err
		}
		d.pkgs = append(d.pkgs, pkg)
	}
	return nil
}

func (d *discovery) standalonePackage(key string) (*model.Package, error) {
	po := d.c.Packages[key]
	label := fmt.Sprintf("package %q", key)
	dir := filepath.Join(d.root, filepath.FromSlash(po.Path))
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", label, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("config: %s: path %q is not a folder", label, po.Path)
	}
	d.owner[key] = ""
	pkg := &model.Package{
		Name:          key,
		Dir:           dir,
		BuildWeight:   1,
		PublishWeight: 1,
		Changelog:     changelogSpec(d.c.Changelog),
		GitHub:        githubSpec(d.c.GitHub),
		Src:           d.c.Src,
	}
	// The entry is the package's whole configuration: a synthetic
	// single-package space built through the same layers as an override, the
	// entry then the in-folder file, so a standalone package can never
	// express something a space package cannot.
	layers := []overrideLayer{{po, label,
		DepSource{KeyPath: []string{"packages", key, "dependencies"}}}}
	filePO, fileSrc, err := loadPackageFile(dir)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", label, err)
	}
	if fileSrc != "" {
		layers = append(layers, overrideLayer{filePO, fmt.Sprintf("%s (%s)", label, fileSrc),
			DepSource{File: fileSrc, KeyPath: []string{"dependencies"}}})
	}
	// A standalone package is its own space, so it starts from the same root
	// defaults every space does, with its path filled in.
	base := rootDefaults(d.c)
	base.Path = po.Path
	if base.Flow == nil {
		base.Flow = &SpaceFlowConfig{}
	}
	merged, ex, autoVersioned, withDeps, err := applyLayers(d.c, base, key, layers, d.declared)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	d.declared = withDeps
	if merged, err = validateSpaceAs(label, merged); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	scope := packageScope(d.c, merged)
	if err := scope.checkSpaceRefs(label, merged); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if pkg.Space, err = buildSpace(d.c, scope, label, key, dir, merged); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if autoVersioned {
		d.onlyChecks = append(d.onlyChecks, onlyCheck{label, merged.AutoVersion})
	}
	applyMerged(pkg, merged, ex)
	if pkg.Ignore, err = packageIgnore(d.baseIgnore, pkg.Dir, ex.ignore); err != nil {
		return nil, fmt.Errorf("config: %s: %w", label, err)
	}
	return pkg, nil
}

// checkAll is everything that can only be asked once every package is known.
func (d *discovery) checkAll(spaceNames []string) error {
	if err := d.checkAutoVersionOnly(spaceNames); err != nil {
		return err
	}
	if err := checkManifestNames(d.pkgs); err != nil {
		return err
	}
	if err := checkSrcFolders(d.pkgs); err != nil {
		return err
	}
	if err := canonicaliseEndpoints(d.declared, d.pkgs); err != nil {
		return err
	}
	if err := checkSpaceDependencies(d.declared, d.owner); err != nil {
		return err
	}
	return checkAliasTagsAreWriteOnly(d.pkgs)
}

// checkAutoVersionOnly holds every autoVersion `only` name to the discovered
// packages: anything else is the same class of typo as an unknown dependency
// endpoint. Only enabled blocks are held to it, since a disabled block is
// inert configuration.
func (d *discovery) checkAutoVersionOnly(spaceNames []string) error {
	for _, sn := range spaceNames {
		av := d.spaceConfigs[sn].AutoVersion
		if av == nil || !av.IsEnabled() {
			continue
		}
		for _, name := range av.Only {
			if _, ok := d.owner[name]; !ok {
				return fmt.Errorf("config: space %q: autoVersion.only: unknown package %q", sn, name)
			}
		}
	}
	for _, chk := range d.onlyChecks {
		if !chk.av.IsEnabled() {
			continue
		}
		for _, name := range chk.av.Only {
			if _, ok := d.owner[name]; !ok {
				return fmt.Errorf("config: %s: autoVersion.only: unknown package %q", chk.label, name)
			}
		}
	}
	return nil
}
