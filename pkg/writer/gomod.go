package writer

import (
	"bytes"
	"fmt"
	"os"

	"golang.org/x/mod/modfile"
)

// rewriteGoMod edits a go.mod's require directives via x/mod's modfile,
// which preserves formatting and comments by design. go.mod has one
// dependency field and no own version, so edits with a named kind are
// missing by definition and the version argument has no target (Rewrite
// ignores it for go.mod). Only modules the file already requires are
// updated: adding a require is dependency management, not version syncing.
func rewriteGoMod(path string, edits []Edit) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	f, err := modfile.Parse(path, data, nil)
	if err != nil {
		return Result{}, err
	}
	required := make(map[string]string, len(f.Require))
	for _, req := range f.Require {
		required[req.Mod.Path] = req.Mod.Version
	}

	var res Result
	for _, e := range edits {
		have, ok := required[e.Name]
		if e.Kind != "" || !ok {
			res.Missing = append(res.Missing, e)
			continue
		}
		if have == e.Range {
			continue // already the wanted version: no change, not missing
		}
		if err := f.AddRequire(e.Name, e.Range); err != nil {
			return res, fmt.Errorf("%s: require %s: %w", path, e.Name, err)
		}
		res.Applied = append(res.Applied, e)
	}
	if len(res.Applied) == 0 {
		return res, nil
	}
	out, err := f.Format()
	if err != nil {
		return res, fmt.Errorf("%s: %w", path, err)
	}
	if bytes.Equal(out, data) {
		return res, nil
	}
	return res, atomicWrite(path, out)
}
