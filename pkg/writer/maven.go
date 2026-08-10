package writer

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// rewriteMaven edits a pom.xml: the project's own <version> and the <version>
// of each <dependency> whose groupId:artifactId coordinate matches an edit.
//
// Only the project's *own* version element is written, never the <parent>'s:
// the parent's version selects which POM this one inherits from, so rewriting
// it would repoint the build at a different parent rather than release this
// module. The scanner falls back to the parent's version when the project
// declares none, but a fallback is a reading, not a place to write.
//
// A version spelled as a property reference (`<version>${core.version}</version>`)
// is left alone and reported missing, matching the scanner's rule that a
// ${property} is kept verbatim: the value lives in a <properties> block or a
// parent POM, and overwriting the reference with a literal would sever it.
func rewriteMaven(path, version string, edits []Edit) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	wanted := make(map[string]int, len(edits))
	for i, e := range edits {
		// A pom expresses kinds as scopes on the dependency itself, which the
		// scanner maps onto kinds; an edit may target any of them, so the
		// coordinate alone identifies the declaration.
		wanted[e.Name] = i
	}
	spans, versionSpan, err := mavenSpans(data, wanted)
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

// mavenSpans locates the version span of each wanted dependency and of the
// project's own version element.
func mavenSpans(data []byte, wanted map[string]int) (map[int]span, *span, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var (
		path        []string
		spans       = make(map[int]span)
		versionSpan *span
	)
	for {
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
				path = path[:len(path)-1]
			}
		case xml.StartElement:
			parent := ""
			if len(path) > 0 {
				parent = path[len(path)-1]
			}
			path = append(path, t.Name.Local)

			switch {
			case t.Name.Local == "dependency":
				coordinate, s, ok, err := mavenDependencyVersion(dec, data)
				if err != nil {
					return nil, nil, err
				}
				path = path[:len(path)-1] // the scan consumed the closing tag
				if i, want := wanted[coordinate]; ok && want {
					spans[i] = s
				}

			// The project's own version is a direct child of the root, so a
			// <parent><version> — one level deeper — can never match.
			case t.Name.Local == "version" && parent == "project" && len(path) == 2:
				s, text, spliceable, err := xmlElementTextSpan(dec, data)
				if err != nil {
					return nil, nil, err
				}
				path = path[:len(path)-1]
				if spliceable && versionSpan == nil && !isDeferredValue(text) {
					s := s
					versionSpan = &s
				}
			}
		}
	}
}

// mavenDependencyVersion reads one <dependency> element whole, returning its
// groupId:artifactId coordinate and the span its <version> text occupies. A
// dependency with no version, or one deferring to a ${property}, reports no
// usable span.
func mavenDependencyVersion(dec *xml.Decoder, data []byte) (coordinate string, s span, ok bool, err error) {
	var group, artifact string
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", span{}, false, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return mavenCoord(group, artifact), s, ok, nil
		case xml.StartElement:
			switch t.Name.Local {
			case "groupId":
				if err := dec.DecodeElement(&group, &t); err != nil {
					return "", span{}, false, err
				}
			case "artifactId":
				if err := dec.DecodeElement(&artifact, &t); err != nil {
					return "", span{}, false, err
				}
			case "version":
				vs, text, spliceable, err := xmlElementTextSpan(dec, data)
				if err != nil {
					return "", span{}, false, err
				}
				if spliceable && !isDeferredValue(text) {
					s, ok = vs, true
				}
			default:
				if err := dec.Skip(); err != nil {
					return "", span{}, false, err
				}
			}
		}
	}
}

// mavenCoord joins group and artifact into the canonical coordinate the
// scanner reports, so an edit naming one addresses the same declaration.
func mavenCoord(group, artifact string) string {
	artifact = strings.TrimSpace(artifact)
	group = strings.TrimSpace(group)
	if artifact == "" {
		return ""
	}
	if group == "" {
		return artifact
	}
	return group + ":" + artifact
}
