package writer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// The O3DE manifest fields the writer touches.
const (
	o3deKeyVersion      = "version"
	o3deKeyDependencies = "dependencies"
	o3deKeyProjectName  = "project_name"
	o3deKeyGemName      = "gem_name"
)

// rewriteO3DE edits an O3DE project.json or gem.json: the manifest's own
// version, and the version text of each dependency named.
//
// O3DE writes a dependency as one string carrying both parts, "Atom==1.0.0",
// so an edit replaces the whole literal rather than a value inside it. The
// gem's name is put back exactly as the file spelled it and the caller's range
// text follows, operator included, which is what makes the write reversible by
// reading the file again.
//
// A file declaring neither project_name nor gem_name is left alone entirely.
// project.json is not a name unique to O3DE, and plenty of other tools keep a
// version under one; writing into those would be the worst thing this format
// could do. The reader answers the same way, reporting nothing rather than a
// wrong identity, and the two have to agree.
func rewriteO3DE(path, version string, edits []Edit) (Result, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return Result{}, err
	}
	versionSpan, deps, named, err := o3deSpans(sp.bytes())
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}
	if !named {
		return Result{Missing: edits}, nil
	}

	var res Result
	for _, e := range edits {
		// O3DE has one dependency list. An edit naming a dev or peer field
		// names something the format cannot express.
		if e.Kind != manifest.KindDependencies {
			res.Missing = append(res.Missing, e)
			continue
		}
		d, ok := deps[e.Name]
		if !ok {
			res.Missing = append(res.Missing, e)
			continue
		}
		want := e.Name + e.Range
		if d.spec == want {
			continue // already the wanted text: no change, not missing
		}
		res.Applied = append(res.Applied, e)
		sp.replace(d.span, quote(want))
	}
	if version != "" && versionSpan != nil {
		if string(current(sp.bytes(), *versionSpan)) != version {
			res.VersionWritten = true
			sp.replace(*versionSpan, quote(version))
		}
	}
	return res, sp.commit(verifyJSON)
}

// o3deDep is one declared dependency: the whole specifier as the file spells
// it, and the span of the string literal holding it.
type o3deDep struct {
	spec string
	span span
}

// o3deSpans locates, in one pass over the token stream, the manifest's own
// version and every dependency specifier, keyed by the gem name each names,
// and reports whether the file names itself as an O3DE project or gem at all.
// A name declared twice keeps its first span, because that is the declaration a
// reader resolves and a second splice over the same bytes would be refused
// anyway.
func o3deSpans(data []byte) (versionSpan *span, deps map[string]o3deDep, named bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, false, err
	}
	if tok != json.Delim('{') {
		return nil, nil, false, fmt.Errorf("top level is not an object")
	}
	deps = make(map[string]o3deDep)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, nil, false, err
		}
		key, _ := keyTok.(string)
		afterKey := dec.InputOffset()
		switch key {
		case o3deKeyProjectName, o3deKeyGemName:
			nameTok, err := dec.Token()
			if err != nil {
				return nil, nil, false, err
			}
			if s, ok := nameTok.(string); ok && s != "" {
				named = true
			}
		case o3deKeyVersion:
			s, err := scalarSpan(data, dec, afterKey)
			if err != nil {
				return nil, nil, false, err
			}
			versionSpan = &s
		case o3deKeyDependencies:
			open, err := dec.Token()
			if err != nil {
				return nil, nil, false, err
			}
			if open != json.Delim('[') {
				return nil, nil, false, fmt.Errorf("%q is not an array", key)
			}
			at := dec.InputOffset()
			for dec.More() {
				elem, err := dec.Token()
				if err != nil {
					return nil, nil, false, err
				}
				end := dec.InputOffset()
				spec, ok := elem.(string)
				if !ok {
					// A non-string element declares nothing this can rewrite.
					at = end
					continue
				}
				start := at
				for start < end && (data[start] == ',' || data[start] == ' ' ||
					data[start] == '\t' || data[start] == '\n' || data[start] == '\r') {
					start++
				}
				if name, _, ok := o3deSpecName(spec); ok {
					if _, seen := deps[name]; !seen {
						deps[name] = o3deDep{spec: spec, span: span{start, end}}
					}
				}
				at = end
			}
			if _, err := dec.Token(); err != nil { // closing ']'
				return nil, nil, false, err
			}
		default:
			if err := skipValue(dec); err != nil {
				return nil, nil, false, err
			}
		}
	}
	return versionSpan, deps, named, nil
}

// o3deSpecName splits a dependency specifier into its gem name and version
// text, spelled the same way the reader spells it. The name is kept verbatim;
// O3DE gem names are capitalised and folding them would stop them matching.
func o3deSpecName(spec string) (name, rng string, ok bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", false
	}
	for i := 0; i < len(spec); i++ {
		if strings.IndexByte("<>=!~", spec[i]) >= 0 {
			return strings.TrimSpace(spec[:i]), strings.TrimSpace(spec[i:]), i > 0
		}
	}
	return spec, "", true
}
