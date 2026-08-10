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

// rewriteCsproj edits an SDK-style .csproj: the project's own <Version>
// property and the version of each <PackageReference> matching an edit.
//
// A package reference spells its version either as an attribute
// (`<PackageReference Include="X" Version="1.0" />`) or as a child element
// (`<Version>1.0</Version>`); both are spliced in place, and the file's own
// formatting is otherwise untouched. A <ProjectReference> declares no version,
// so an edit naming one is missing — as is any edit carrying a named kind,
// since a .csproj has one dependency field.
//
// A version spelled as an MSBuild property reference (`$(VersionPrefix)`) is
// left alone and reported missing: the value lives in a Directory.Build.props
// or on the command line, and freezing it to a literal would stop the property
// working.
//
// Where several PropertyGroups declare a Version — they are usually
// conditioned on a configuration — only the first is written, which is exactly
// the one the scanner reports, so the two halves cannot disagree about what
// this project's version is.
func rewriteCsproj(path, version string, edits []Edit) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	wanted := make(map[string]int, len(edits))
	for i, e := range edits {
		if e.Kind == "" || e.Kind == "dependencies" {
			wanted[e.Name] = i
		}
	}
	spans, versionSpan, err := csprojSpans(data, wanted)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}

	type patch struct {
		span
		text string
	}
	var (
		res     Result
		patches []patch
	)
	for i, e := range edits {
		s, ok := spans[i]
		if !ok {
			res.Missing = append(res.Missing, e)
			continue
		}
		if string(data[s.start:s.end]) == e.Range {
			continue // already the wanted text: no change, not missing
		}
		res.Applied = append(res.Applied, e)
		patches = append(patches, patch{s, e.Range})
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

	// Splice back to front so earlier offsets stay valid.
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

// csprojSpans locates, in one pass over the token stream, the version span of
// each wanted PackageReference and of the project's own Version property.
func csprojSpans(data []byte, wanted map[string]int) (map[int]span, *span, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var (
		path        []string
		spans       = make(map[int]span)
		versionSpan *span
		pending     = -1 // the PackageReference awaiting its <Version> child
	)
	for {
		prev := dec.InputOffset()
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return spans, versionSpan, nil
		}
		if err != nil {
			return nil, nil, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if len(path) > 0 {
				if path[len(path)-1] == "PackageReference" {
					pending = -1
				}
				path = path[:len(path)-1]
			}
		case xml.StartElement:
			parent := ""
			if len(path) > 0 {
				parent = path[len(path)-1]
			}
			path = append(path, t.Name.Local)

			switch {
			case t.Name.Local == "PackageReference":
				i, want := wanted[xmlAttr(t, "Include")]
				if !want {
					break
				}
				// The attribute form is complete on the start tag itself; the
				// element form is picked up by the <Version> child below.
				if xmlHasAttr(t, "Version") {
					s, ok := attrValueSpan(data[prev:dec.InputOffset()], prev, "Version")
					if ok && !isDeferredValue(string(data[s.start:s.end])) {
						spans[i] = s
					}
					break
				}
				pending = i

			case t.Name.Local == "Version" && parent == "PropertyGroup":
				s, text, spliceable, err := xmlElementTextSpan(dec, data)
				if err != nil {
					return nil, nil, err
				}
				path = path[:len(path)-1] // the span consumed the closing tag
				if spliceable && versionSpan == nil && !isDeferredValue(text) {
					s := s
					versionSpan = &s
				}

			case t.Name.Local == "Version" && parent == "PackageReference" && pending >= 0:
				s, text, spliceable, err := xmlElementTextSpan(dec, data)
				if err != nil {
					return nil, nil, err
				}
				path = path[:len(path)-1]
				if spliceable && !isDeferredValue(text) {
					spans[pending] = s
				}
			}
		}
	}
}

// xmlAttr reads one attribute's value by local name, whatever namespace
// prefix precedes it.
func xmlAttr(e xml.StartElement, local string) string {
	for _, a := range e.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// xmlHasAttr reports an attribute's presence by local name.
func xmlHasAttr(e xml.StartElement, local string) bool {
	for _, a := range e.Attr {
		if a.Name.Local == local {
			return true
		}
	}
	return false
}
