package writer

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// rewriteNuspec edits a .nuspec — NuGet's own package manifest: the package's
// <version> and the version attribute of each <dependency> matching an edit,
// whether it sits flat or inside a targetFramework <group>.
//
// A nuspec may be a template rather than a finished manifest:
// `<version>$version$</version>` is a token NuGet fills in from the project at
// pack time. Writing a literal over it would sever that link and freeze the
// package at whatever was written, so a token value is left alone and reported
// missing — the same rule the plist writer applies to $(MARKETING_VERSION).
func rewriteNuspec(path, version string, edits []Edit) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	wanted := make(map[string]int, len(edits))
	for i, e := range edits {
		// A nuspec has one dependency field; a group is a target framework,
		// not a kind.
		if e.Kind == "" || e.Kind == "dependencies" {
			wanted[e.Name] = i
		}
	}

	type patch struct {
		span
		text string
	}
	var (
		res         Result
		patches     []patch
		seen        = make(map[int]bool, len(edits))
		found       = make(map[int]bool, len(edits))
		applied     = make(map[int]bool, len(edits))
		versionSpan *span
		path0       []string
		dec         = xml.NewDecoder(bytes.NewReader(data))
	)
	for {
		prev := dec.InputOffset()
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", path, err)
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if len(path0) > 0 {
				path0 = path0[:len(path0)-1]
			}
		case xml.StartElement:
			parent := ""
			if len(path0) > 0 {
				parent = path0[len(path0)-1]
			}
			path0 = append(path0, t.Name.Local)

			switch {
			case t.Name.Local == "dependency":
				i, want := wanted[xmlAttr(t, "id")]
				if !want {
					break
				}
				seen[i] = true
				if !xmlHasAttr(t, "version") {
					break
				}
				s, ok := attrValueSpan(data[prev:dec.InputOffset()], prev, "version")
				if !ok || isDeferredValue(string(data[s.start:s.end])) {
					break
				}
				found[i] = true
				if string(data[s.start:s.end]) == edits[i].Range {
					break // already the wanted text: no change, not missing
				}
				applied[i] = true
				patches = append(patches, patch{s, edits[i].Range})

			// The package's own version is a direct child of <metadata>, which
			// keeps a <dependency> element's version out of the running.
			case t.Name.Local == "version" && parent == "metadata":
				s, text, spliceable, err := xmlElementTextSpan(dec, data)
				if err != nil {
					return Result{}, fmt.Errorf("%s: %w", path, err)
				}
				path0 = path0[:len(path0)-1] // the span consumed the closing tag
				if spliceable && versionSpan == nil && !isDeferredValue(text) {
					s := s
					versionSpan = &s
				}
			}
		}
	}

	for i, e := range edits {
		switch {
		case applied[i]:
			res.Applied = append(res.Applied, e)
		case found[i]:
		case seen[i]:
			res.Skipped = append(res.Skipped, e)
		default:
			res.Missing = append(res.Missing, e)
		}
	}
	if version != "" && versionSpan != nil {
		if string(data[versionSpan.start:versionSpan.end]) != version {
			res.VersionWritten = true
			patches = append(patches, patch{*versionSpan, version})
		}
	}
	if len(patches) == 0 {
		return res, nil
	}

	sort.Slice(patches, func(i, j int) bool { return patches[i].start > patches[j].start })
	out := data
	for _, p := range patches {
		var escaped bytes.Buffer
		if err := xml.EscapeText(&escaped, []byte(p.text)); err != nil {
			return res, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out[:p.start], append(escaped.Bytes(), out[p.end:]...)...)
	}
	if err := xmlWellFormed(out); err != nil {
		return res, fmt.Errorf("%s: internal error: rewrite produced invalid XML: %w", path, err)
	}
	return res, atomicWrite(path, out)
}
