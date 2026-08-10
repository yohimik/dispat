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

// rewritePackagesProps edits a Directory.Packages.props: the central version
// of each package matching an edit. Central Package Management is where a
// repository pins every version once and its projects reference packages
// without one, so this file is the only place those versions can be changed —
// and, like a Gradle version catalog, it declares no identity and no kinds.
func rewritePackagesProps(path string, edits []Edit) (Result, error) {
	return rewriteXMLPackageList(path, edits, "PackageVersion", "Include", "Version")
}

// rewritePackagesConfig edits a legacy packages.config. Its attributes are
// lower-case, unlike every other MSBuild-adjacent format, and it declares no
// version of its own.
func rewritePackagesConfig(path string, edits []Edit) (Result, error) {
	return rewriteXMLPackageList(path, edits, "package", "id", "version")
}

// rewriteXMLPackageList is the splice both flat NuGet dependency lists share:
// find each element named elem whose identity attribute names an edit, and
// replace the bytes between the quotes of its version attribute. The two
// formats differ only in what those three names are called.
//
// A version spelled as an MSBuild property reference (`$(SerilogVersion)`) is
// left alone: freezing it to a literal would stop the property working.
//
// A package declared more than once — the same entry repeated under two
// conditioned ItemGroups — is spliced in every place and reported once.
func rewriteXMLPackageList(path string, edits []Edit, elem, idAttr, versionAttr string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	wanted := make(map[string]int, len(edits))
	for i, e := range edits {
		// These files have one dependency field, so a kinded edit — beyond the
		// long spelling of the zero kind — names something they cannot express.
		if e.Kind == "" || e.Kind == "dependencies" {
			wanted[e.Name] = i
		}
	}

	type patch struct {
		span
		text string
	}
	var (
		res     Result
		patches []patch
		found   = make(map[int]bool, len(edits))
		applied = make(map[int]bool, len(edits))
		dec     = xml.NewDecoder(bytes.NewReader(data))
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
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != elem {
			continue
		}
		i, want := wanted[xmlAttr(start, idAttr)]
		if !want || !xmlHasAttr(start, versionAttr) {
			continue
		}
		s, ok := attrValueSpan(data[prev:dec.InputOffset()], prev, versionAttr)
		if !ok || isDeferredValue(string(data[s.start:s.end])) {
			continue
		}
		found[i] = true
		if string(data[s.start:s.end]) == edits[i].Range {
			continue // already the wanted text: no change, not missing
		}
		applied[i] = true
		patches = append(patches, patch{s, edits[i].Range})
	}
	// Reported in edit order rather than document order, so the result does not
	// depend on where in the file a declaration happens to sit.
	for i, e := range edits {
		switch {
		case applied[i]:
			res.Applied = append(res.Applied, e)
		case !found[i]:
			res.Missing = append(res.Missing, e)
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
