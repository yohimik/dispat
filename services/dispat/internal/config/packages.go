package config

// Per-package configuration and versioning groups. A space package's
// effective configuration is its space's, overlaid with up to two override
// layers — its top-level `packages` entry, then the package folder's own
// dispat config file — most local winning field by field; a standalone
// package (a `packages` entry with a path) starts from a synthetic base
// instead of a space. The merge produces an ordinary space-shaped config,
// validated with the same helpers as a space, so a package can never express
// something a space cannot.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// DispatignoreName is the per-space ignore file: one pattern per line,
// matched against the space folder's direct sub-folder names, marking
// folders that are not packages. Blank lines and #-comments are skipped;
// "*" in a pattern matches any run of characters.
const DispatignoreName = ".dispatignore"

// validateVersionGroups normalizes the declared groups in place and rejects
// the two declaration mistakes: a versioning mode that does not share (a
// group exists to share versions), and a name a space already holds — group
// and space names share one namespace, because a versionGroup reference may
// name either.
func validateVersionGroups(c *File) error {
	for name, g := range c.VersionGroups {
		if name == "" {
			return errors.New("versionGroups: group name must not be empty")
		}
		if _, taken := c.Spaces[name]; taken {
			return fmt.Errorf("versionGroups[%q]: a space has the same name; group and space names share one namespace", name)
		}
		mode, ok := normalizeVersioning(g.Versioning)
		if !ok || !model.Versioning(mode).Shared() {
			return fmt.Errorf("versionGroups[%q]: versioning %q is invalid (a group exists to share versions; want %q or %q)",
				name, g.Versioning, VersioningFixed, VersioningFixedSparse)
		}
		g.Versioning = mode
		c.VersionGroups[name] = g
	}
	return nil
}

// resolveVersionGroup resolves a versionGroup reference onto the group key
// and versioning mode it stands for: a declared versionGroups entry, or a
// space whose own versioning is shared (its implicit group). The lookup is
// case-insensitive because viper lowercases both maps' keys.
func resolveVersionGroup(c *File, ref string) (key, mode string, err error) {
	low := strings.ToLower(ref)
	if g, ok := c.VersionGroups[low]; ok {
		return low, g.Versioning, nil
	}
	if s, ok := c.Spaces[low]; ok {
		if s.VersionGroup != "" {
			return "", "", fmt.Errorf("versionGroup %q: space %q is itself a member of group %q; name that group directly",
				ref, low, s.VersionGroup)
		}
		if !model.Versioning(s.Versioning).Shared() {
			return "", "", fmt.Errorf("versionGroup %q: space %q does not version as a group (its versioning is %q)",
				ref, low, s.Versioning)
		}
		return low, s.Versioning, nil
	}
	return "", "", fmt.Errorf("versionGroup %q matches no versionGroups entry and no space", ref)
}

// resolveSpaceVersioning returns the effective versioning mode and group key
// of a validated space-shaped config: the referenced group's when one is
// named, the config's own mode under the space's implicit group otherwise.
func resolveSpaceVersioning(c *File, spaceName string, sc SpaceConfig) (mode, group string, err error) {
	if sc.VersionGroup != "" {
		group, mode, err = resolveVersionGroup(c, sc.VersionGroup)
		return mode, group, err
	}
	return sc.Versioning, spaceName, nil
}

// validatePackageEntries checks the top-level packages map on its own: keys
// must be named, and a standalone entry's path must stay inside the
// repository. Whether a key without a path matches a package folder needs
// the folders on disk and is checked in discovery instead.
func validatePackageEntries(c *File) error {
	for name, po := range c.Packages {
		if name == "" {
			return errors.New("packages: package name must not be empty")
		}
		if po.Path == "" {
			continue
		}
		if filepath.IsAbs(po.Path) {
			return fmt.Errorf("packages[%q]: path %q must be a repository-relative path", name, po.Path)
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(po.Path)))
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("packages[%q]: path %q escapes the repository root", name, po.Path)
		}
		if clean == "." {
			return fmt.Errorf("packages[%q]: path %q must name a folder inside the repository", name, po.Path)
		}
	}
	return nil
}

// collectPackageDeps appends one package layer's provider names to the
// declared-edge list: the consumer is the package itself and the kind is the
// default. The source's Index is filled per entry.
func collectPackageDeps(declared []DeclaredDependency, pkg string, src DepSource, providers []string) ([]DeclaredDependency, error) {
	for i, p := range providers {
		src.Index = i
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("%s: provider name must not be empty", src.Label())
		}
		if strings.EqualFold(p, pkg) {
			return nil, fmt.Errorf("%s: package %q cannot depend on itself", src.Label(), pkg)
		}
		declared = append(declared, DeclaredDependency{
			DependencyConfig: DependencyConfig{Consumer: pkg, Provider: p},
			Source:           src,
		})
	}
	return declared, nil
}

// validatePackageLayer checks what can be wrong with one override layer on
// its own, before it is merged.
func validatePackageLayer(label string, po PackageConfig) error {
	if po.Flow != nil && po.Flow.Login != nil {
		return fmt.Errorf("%s: flow.login cannot be overridden per package: login runs once per space, in the space folder, gating every publish of the space", label)
	}
	if po.Versioning != "" && po.VersionGroup != "" {
		return fmt.Errorf("%s: versioning and versionGroup are mutually exclusive (the group's versioning is authoritative)", label)
	}
	if len(po.Concurrency) > 2 {
		return fmt.Errorf("%s: concurrency accepts at most two values [build, publish], got %v", label, po.Concurrency)
	}
	for _, v := range po.Concurrency {
		if v < 0 {
			return fmt.Errorf("%s: concurrency values must be >= 0, got %v", label, po.Concurrency)
		}
	}
	return nil
}

// mergePackageOverride overlays one override layer onto a space-shaped
// config. Versioning and versionGroup are one axis — a layer setting either
// supersedes both inherited values — flow merges entry by entry, scripts
// name by name, and autoVersion replaces wholesale: its empty fields already
// carry meaning relative to their siblings (no kinds means all four), so an
// overlay could never express them against a non-empty base.
func mergePackageOverride(sc SpaceConfig, po PackageConfig) SpaceConfig {
	if po.IsBuildWaitingPublish != nil {
		sc.IsBuildWaitingPublish = *po.IsBuildWaitingPublish
	}
	if po.RevertOnFail != nil {
		sc.RevertOnFail = *po.RevertOnFail
	}
	if po.TagFormat != "" {
		sc.TagFormat = po.TagFormat
	}
	if po.Versioning != "" {
		sc.Versioning = po.Versioning
		sc.VersionGroup = ""
	}
	if po.VersionGroup != "" {
		sc.VersionGroup = po.VersionGroup
		sc.Versioning = ""
	}
	if po.Flow != nil {
		sc.Flow = mergeFlow(sc.Flow, po.Flow)
	}
	if len(po.Scripts) > 0 {
		merged := make(map[string]string, len(sc.Scripts)+len(po.Scripts))
		for k, v := range sc.Scripts {
			merged[k] = v
		}
		for k, v := range po.Scripts {
			merged[k] = v
		}
		sc.Scripts = merged
	}
	if po.AutoVersion != nil {
		sc.AutoVersion = po.AutoVersion
	}
	return sc
}

// mergeFlow overlays flow entries one by one: a nil entry inherits, a
// non-nil one — the explicit empty array included — replaces, which is how
// an override clears an inherited stage. Login is not merged; validation
// rejects it on the override layer.
func mergeFlow(base, over *SpaceFlowConfig) *SpaceFlowConfig {
	out := *base
	pick := func(dst *[]string, src []string) {
		if src != nil {
			*dst = src
		}
	}
	pick(&out.Build, over.Build)
	pick(&out.Publish, over.Publish)
	pick(&out.Version, over.Version)
	pick(&out.Announce, over.Announce)
	pick(&out.BeforeAll, over.BeforeAll)
	pick(&out.BeforeVersion, over.BeforeVersion)
	pick(&out.PostVersion, over.PostVersion)
	pick(&out.BeforeBuild, over.BeforeBuild)
	pick(&out.PostBuild, over.PostBuild)
	pick(&out.BeforePublish, over.BeforePublish)
	pick(&out.PostPublish, over.PostPublish)
	pick(&out.BeforeAnnounce, over.BeforeAnnounce)
	pick(&out.PostAnnounce, over.PostAnnounce)
	pick(&out.OnFail, over.OnFail)
	pick(&out.OnSkip, over.OnSkip)
	return &out
}

// packageExtras carries the package-only override knobs — the keys that are
// not space-shaped — across the layers.
type packageExtras struct {
	changelog     *ChangelogConfig
	github        *GitHubConfig
	concurrency   []int
	manifestNames []string
}

// apply folds one layer's package-only keys in. Changelog and github overlay
// the top-level objects field by field (the first layer starts from the
// global config), so a package can flip enabled and keep the global titles,
// or point at another repository and keep the global tokenEnv.
func (ex *packageExtras) apply(c *File, po PackageConfig) {
	if po.Changelog != nil {
		base := c.Changelog
		if base == nil {
			base = &ChangelogConfig{}
		}
		if ex.changelog != nil {
			base = ex.changelog
		}
		ex.changelog = overlayChangelog(base, po.Changelog)
	}
	if po.GitHub != nil {
		base := c.GitHub
		if base == nil {
			base = &GitHubConfig{}
		}
		if ex.github != nil {
			base = ex.github
		}
		ex.github = overlayGitHub(base, po.GitHub)
	}
	if po.Concurrency != nil {
		ex.concurrency = po.Concurrency
	}
	if len(po.ManifestNames) > 0 {
		// A list replaces rather than appends: the layer nearest the package
		// states what the package is called, and adding to an inherited list
		// could never take a name away again.
		ex.manifestNames = po.ManifestNames
	}
}

func overlayFormat(base, over EntryFormatConfig) EntryFormatConfig {
	if over.DateFormat != "" {
		base.DateFormat = over.DateFormat
	}
	if over.BreakingTitle != "" {
		base.BreakingTitle = over.BreakingTitle
	}
	if over.FeaturesTitle != "" {
		base.FeaturesTitle = over.FeaturesTitle
	}
	if over.FixesTitle != "" {
		base.FixesTitle = over.FixesTitle
	}
	if over.DependenciesTitle != "" {
		base.DependenciesTitle = over.DependenciesTitle
	}
	return base
}

func overlayChangelog(base, over *ChangelogConfig) *ChangelogConfig {
	out := *base
	if over.Enabled != nil {
		out.Enabled = over.Enabled
	}
	if over.File != "" {
		out.File = over.File
	}
	if over.Title != "" {
		out.Title = over.Title
	}
	out.EntryFormatConfig = overlayFormat(out.EntryFormatConfig, over.EntryFormatConfig)
	return &out
}

func overlayGitHub(base, over *GitHubConfig) *GitHubConfig {
	out := *base
	if over.Enabled != nil {
		out.Enabled = over.Enabled
	}
	if over.Owner != "" {
		out.Owner = over.Owner
	}
	if over.Repo != "" {
		out.Repo = over.Repo
	}
	if over.APIURL != "" {
		out.APIURL = over.APIURL
	}
	if over.TokenEnv != "" {
		out.TokenEnv = over.TokenEnv
	}
	if over.AllPackages != nil {
		out.AllPackages = over.AllPackages
	}
	out.EntryFormatConfig = overlayFormat(out.EntryFormatConfig, over.EntryFormatConfig)
	return &out
}

// packageWeights resolves a package's concurrency override onto its stage
// weights. The shape was validated with the layer; absence and 0 mean 1,
// the ordinary cost — deliberately unlike the top-level key, where 0 means
// the number of CPUs, because a weight has no CPU reading.
func packageWeights(conc []int) (build, publish int) {
	build, publish = 1, 1
	switch len(conc) {
	case 1:
		build, publish = conc[0], conc[0]
	case 2:
		build, publish = conc[0], conc[1]
	}
	return max(1, build), max(1, publish)
}

// recordFormat maps the config entry-format options onto the resolved model.
func recordFormat(f EntryFormatConfig) model.RecordFormat {
	return model.RecordFormat{
		DateFormat:        f.DateFormat,
		BreakingTitle:     f.BreakingTitle,
		FeaturesTitle:     f.FeaturesTitle,
		FixesTitle:        f.FixesTitle,
		DependenciesTitle: f.DependenciesTitle,
	}
}

// changelogSpec resolves a changelog config onto a package's record policy.
// Nil-safe like IsEnabled: DiscoverPackages accepts configs that skipped
// Load's optional-object filling.
func changelogSpec(cc *ChangelogConfig) model.ChangelogSpec {
	if cc == nil {
		cc = &ChangelogConfig{}
	}
	return model.ChangelogSpec{
		Enabled: cc.IsEnabled(),
		File:    cc.File,
		Title:   cc.Title,
		Format:  recordFormat(cc.EntryFormatConfig),
	}
}

// githubSpec resolves a github config onto a package's record policy.
// Nil-safe, for the same reason as changelogSpec.
func githubSpec(gc *GitHubConfig) model.GitHubSpec {
	if gc == nil {
		gc = &GitHubConfig{}
	}
	return model.GitHubSpec{
		Enabled:     gc.IsEnabled(),
		AllPackages: gc.AllPackagesEnabled(),
		Owner:       gc.Owner,
		Repo:        gc.Repo,
		APIURL:      gc.APIURL,
		TokenEnv:    gc.TokenEnv,
		Format:      recordFormat(gc.EntryFormatConfig),
	}
}

// loadPackageFile probes a package folder for its in-folder dispat config
// file — the same names and formats the root config resolves through — and
// decodes it as a PackageConfig, the file's top-level object. A file that
// declares spaces or packages is refused with guidance: the folder holds a
// monorepo of its own, and a nested root must be ignored, not merged.
func loadPackageFile(dir string) (PackageConfig, string, error) {
	var pc PackageConfig
	for _, cand := range defaultFileNames {
		p := filepath.Join(dir, cand)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		v := viper.New()
		v.SetConfigFile(p)
		if err := v.ReadInConfig(); err != nil {
			return pc, p, fmt.Errorf("cannot read %s: %w", p, err)
		}
		for _, rootKey := range []string{"spaces", "packages"} {
			if v.IsSet(rootKey) {
				return pc, p, fmt.Errorf(
					"%s declares %s, so the folder looks like a nested monorepo root rather than a package; ignore the folder (%s) or remove the file",
					p, rootKey, DispatignoreName)
			}
		}
		// The same decoding stance as the root config: unknown keys are
		// rejected, weak typing lifts scalar flow entries into slices.
		weak := func(dc *mapstructure.DecoderConfig) { dc.WeaklyTypedInput = true }
		if err := v.UnmarshalExact(&pc, weak); err != nil {
			return pc, p, fmt.Errorf("invalid format in %s: %w", p, err)
		}
		if pc.Path != "" {
			return pc, p, fmt.Errorf(
				"%s: path cannot be set in a package folder's config file — the package's location is the folder itself (or its packages entry's path)",
				p)
		}
		return pc, p, nil
	}
	return pc, "", nil
}

// loadIgnore reads a space folder's .dispatignore patterns; an absent file
// means none.
func loadIgnore(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, DispatignoreName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var patterns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns, nil
}

// ignoredName reports whether a folder name matches one of the space's
// .dispatignore patterns.
func ignoredName(patterns []string, name string) bool {
	for _, p := range patterns {
		if globx.Match(p, name) {
			return true
		}
	}
	return false
}
