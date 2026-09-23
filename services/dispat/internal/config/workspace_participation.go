// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DisabledRepository is one source repository the control inventory excluded
// from this run. Only the declared `.gitmodules` identity and the filesystem
// boundary survive the exclusion: the repository contributes no packages,
// commits, tags, baselines, scripts, records or locks, while the paths it
// occupies remain reserved for it.
type DisabledRepository struct {
	// Name is the exact `.gitmodules` identity.
	Name string
	// Path is the slash-spelled gitlink path relative to the control root.
	Path string
	// Root is the absolute checkout location, which need not exist.
	Root string
}

// participation is the resolved exclusion decision for one composition. It is
// computed from `.gitmodules` text and the control configuration alone, before
// any repository is initialized, inspected or locked.
type participation struct {
	disabled []DisabledRepository
	// packages maps a folded package name to the repository whose exclusion
	// removed it, so a later dependency on that name can say why it is gone.
	packages map[string]string
	// spaces names the excluded space declarations, for the composition log.
	spaces []string
}

// DisabledRepositories returns the excluded sources in `.gitmodules` name
// order.
func (w *Workspace) DisabledRepositories() []DisabledRepository {
	if w == nil {
		return nil
	}
	return w.disabled
}

// DisabledRepositoryNames returns the excluded source identities in name
// order, for diagnostics and structured logs.
func (w *Workspace) DisabledRepositoryNames() []string {
	if w == nil {
		return nil
	}
	names := make([]string, 0, len(w.disabled))
	for _, repo := range w.disabled {
		names = append(names, repo.Name)
	}
	return names
}

// ExcludedPackageRepository reports the excluded repository that would have
// supplied a package of this name. The lookup is best effort: it answers for
// a name the control configuration declared inside an excluded boundary, or a
// folder that boundary still holds on disk.
func (w *Workspace) ExcludedPackageRepository(name string) (string, bool) {
	if w == nil || len(w.excludedPackages) == 0 {
		return "", false
	}
	repository, ok := w.excludedPackages[strings.ToLower(name)]
	return repository, ok
}

// unknownPackageRemedy explains an unknown dependency endpoint that an
// exclusion can account for. It names the exact repository when the name is
// attributable and otherwise lists what this run excluded, so the reader is
// never left to guess whether participation is the cause.
func (w *Workspace) unknownPackageRemedy(name string) string {
	if w == nil || len(w.disabled) == 0 {
		return ""
	}
	if repository, ok := w.ExcludedPackageRepository(name); ok {
		return fmt.Sprintf("; repository %q is excluded by repositoryOverrides "+
			"(set its enabled to true, remove the dependency, or declare it external)", repository)
	}
	return fmt.Sprintf("; this run excluded %s through repositoryOverrides",
		quotedNames(w.DisabledRepositoryNames()))
}

// ownerOf returns the excluded repository whose reserved boundary contains
// the absolute path, or nil. It reads `.gitmodules` text alone, so an excluded
// checkout need not exist or be initialized.
func (p *participation) ownerOf(path string) *DisabledRepository {
	if p == nil || !filepath.IsAbs(path) {
		return nil
	}
	clean := filepath.Clean(path)
	var best *DisabledRepository
	for i := range p.disabled {
		repo := &p.disabled[i]
		if within(repo.Root, clean) && (best == nil || len(repo.Root) > len(best.Root)) {
			best = repo
		}
	}
	return best
}

// resolveParticipation removes every control declaration that belongs to an
// excluded repository and records what the removal took away.
//
// A space path inside an excluded boundary hosts that repository's packages,
// so the path stops contributing and a space left with no path at all is
// dropped with its own package entries. A package declared with a path inside
// an excluded boundary is left alone: the control repository may not own files
// whose history belongs to a repository that is not participating, and package
// ownership reports that crossing in its own words.
func resolveParticipation(cfg *File, controlRoot string, disabled []DisabledRepository) *participation {
	p := &participation{disabled: disabled, packages: map[string]string{}}
	if len(disabled) == 0 {
		return p
	}
	sort.Slice(p.disabled, func(i, j int) bool { return p.disabled[i].Name < p.disabled[j].Name })
	for name := range cfg.Spaces {
		space := cfg.Spaces[name]
		kept := make(PathList, 0, len(space.Path))
		var removedBy string
		for _, declared := range space.Path {
			abs := declared
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(controlRoot, filepath.FromSlash(declared))
			}
			repo := p.ownerOf(filepath.Clean(abs))
			if repo == nil {
				kept = append(kept, declared)
				continue
			}
			removedBy = repo.Name
			p.recordExcludedFolders(filepath.Clean(abs), repo.Name)
		}
		if removedBy == "" {
			continue
		}
		p.spaces = append(p.spaces, name)
		if len(kept) > 0 {
			space.Path = kept
			cfg.Spaces[name] = space
			continue
		}
		for declared := range space.Packages {
			p.packages[strings.ToLower(declared)] = removedBy
		}
		delete(cfg.Spaces, name)
	}
	sort.Strings(p.spaces)
	// A top-level entry without a path configures a package discovered in a
	// space folder. Its space is gone, so the entry would be reported as
	// matching no folder; the exclusion that removed the folder removes it.
	for name := range cfg.Packages {
		if cfg.Packages[name].Path != "" {
			continue
		}
		if _, excluded := p.packages[strings.ToLower(name)]; excluded {
			delete(cfg.Packages, name)
		}
	}
	return p
}

// recordExcludedFolders attributes the package folders an excluded space path
// still holds on disk. A missing path is normal: an excluded repository need
// not be initialized, and the attribution is only used to explain a later
// unknown dependency endpoint.
func (p *participation) recordExcludedFolders(dir, repository string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		p.packages[strings.ToLower(entry.Name())] = repository
	}
}
