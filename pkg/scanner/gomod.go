package scanner

import (
	"strings"

	"golang.org/x/mod/modfile"
)

// parseGoMod reads a go.mod: the module path is the package's name (go.mod
// declares no own version), direct require directives are its dependencies.
// Indirect requires are transitive bookkeeping, not declarations, and are
// skipped — unless a replace directive points the module at a relative
// filesystem path, which is a deliberate local wiring worth an edge either
// way. Go declares exact versions, so Range is the required version verbatim.
func parseGoMod(rel string, data []byte) (Manifest, error) {
	// Parse first for full fidelity (ParseLax drops replace directives, and
	// relative replaces are this parser's strongest workspace signal). When
	// strict parsing fails — typically a directive newer than the vendored
	// x/mod — fall back to ParseLax rather than refuse the manifest: the
	// requires survive, only replace-derived local paths are lost.
	f, err := modfile.Parse(rel, data, nil)
	if err != nil {
		var laxErr error
		if f, laxErr = modfile.ParseLax(rel, data, nil); laxErr != nil {
			return Manifest{}, err // the strict error names the real problem
		}
	}
	m := Manifest{
		Path:      rel,
		Ecosystem: EcosystemGoMod,
		Root:      isRoot(rel),
	}
	if f.Module != nil {
		m.Name = f.Module.Mod.Path
	}
	// Relative-path replaces, keyed by the replaced module path. A replace
	// with a version-specific left side collapses onto the path: for
	// workspace-edge purposes the target folder is the signal, whichever
	// version it replaces.
	local := make(map[string]string)
	for _, rep := range f.Replace {
		if rep.New.Version == "" && isRelativePath(rep.New.Path) {
			local[rep.Old.Path] = rep.New.Path
		}
	}
	for _, req := range f.Require {
		path := req.Mod.Path
		if req.Indirect && local[path] == "" {
			continue
		}
		m.Deps = append(m.Deps, DeclaredDep{
			Name:      path,
			Range:     req.Mod.Version,
			Kind:      KindDependencies,
			LocalPath: local[path],
		})
	}
	sortDeps(m.Deps)
	return m, nil
}

// isRelativePath reports a replace target that is a relative filesystem path
// (the only replace form that can point inside the repository).
func isRelativePath(p string) bool {
	return p == "." || p == ".." ||
		strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../")
}
