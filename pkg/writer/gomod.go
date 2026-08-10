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

// replaceGoMod adds, repoints and removes replace directives. x/mod's modfile
// owns the formatting, the same reason rewriteGoMod uses it: AddReplace edits
// an existing directive in place and appends a new one in the file's own
// style, DropReplace removes it, and Cleanup tidies the block a removal can
// leave behind.
//
// A replacement carrying a Version narrows the directive to that required
// version, which is go.mod's own `replace acme/core v1.2.0 => ../core` form.
// The other formats have no such notion, so this is the only writer that reads
// the field.
func replaceGoMod(path string, replacements []Replacement) (ReplaceResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ReplaceResult{}, err
	}
	f, err := modfile.Parse(path, data, nil)
	if err != nil {
		return ReplaceResult{}, fmt.Errorf("%s: %w", path, err)
	}
	// The directives already in the file, so a redirect that is already what
	// was asked for counts as no change rather than as work done.
	existing := make(map[string]string, len(f.Replace))
	for _, rep := range f.Replace {
		existing[rep.Old.Path+"\x00"+rep.Old.Version] = rep.New.Path
	}

	var res ReplaceResult
	for _, r := range replacements {
		key := r.Name + "\x00" + r.Version
		current, declared := existing[key]
		switch {
		case r.Path == "" && !declared:
			res.Missing = append(res.Missing, r)
		case r.Path == "":
			if err := f.DropReplace(r.Name, r.Version); err != nil {
				return res, fmt.Errorf("%s: drop replace %s: %w", path, r.Name, err)
			}
			res.Applied = append(res.Applied, r)
		case declared && current == r.Path:
			// Already pointing there.
		default:
			if err := f.AddReplace(r.Name, r.Version, r.Path, ""); err != nil {
				return res, fmt.Errorf("%s: replace %s: %w", path, r.Name, err)
			}
			res.Applied = append(res.Applied, r)
		}
	}
	if len(res.Applied) == 0 {
		return res, nil
	}

	f.Cleanup()
	out, err := f.Format()
	if err != nil {
		return res, fmt.Errorf("%s: %w", path, err)
	}
	if bytes.Equal(out, data) {
		return res, nil
	}
	return res, atomicWrite(path, out)
}
