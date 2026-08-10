package scanner

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// newXMLDecoder builds the charset-tolerant decoder every XML format in the
// package reads through, tolerating the single-byte encodings legacy Maven
// and Visual Studio files still declare (ISO-8859-1, windows-1252): Go's
// decoder refuses any non-UTF-8 declaration when no CharsetReader is set, and
// a scanner should read what the tools wrote. windows-1252 is treated as
// latin-1; the 0x80-0x9F range differs between the two, but identifiers never
// use it and mojibake in free text is harmless here.
//
// The struct-decoding formats go through decodeXML; the plist walk drives the
// token stream itself, because only the top-level dictionary's keys count.
func newXMLDecoder(data []byte) *xml.Decoder {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		switch strings.ToLower(charset) {
		case "utf-8", "utf8", "us-ascii", "ascii":
			return input, nil
		case "iso-8859-1", "iso8859-1", "latin1", "windows-1252", "cp1252":
			raw, err := io.ReadAll(input)
			if err != nil {
				return nil, err
			}
			var b bytes.Buffer
			b.Grow(len(raw))
			for _, c := range raw {
				b.WriteRune(rune(c))
			}
			return &b, nil
		}
		return nil, fmt.Errorf("unsupported XML encoding %q", charset)
	}
	return dec
}

// decodeXML unmarshals an XML manifest through the shared decoder.
func decodeXML(data []byte, v any) error {
	return newXMLDecoder(data).Decode(v)
}

// mavenManifest is the subset of pom.xml the scanner reads. groupId and
// version may live on the parent instead of the project itself.
type mavenManifest struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Parent     struct {
		GroupID string `xml:"groupId"`
		Version string `xml:"version"`
	} `xml:"parent"`
	Dependencies []mavenDep `xml:"dependencies>dependency"`
}

type mavenDep struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope"`
	Optional   string `xml:"optional"`
}

// parseMaven reads a pom.xml. Names are the full "groupId:artifactId"
// coordinates — artifactId alone collides across groups. Kinds: scope test
// maps onto devDependencies, an optional dependency onto
// optionalDependencies, everything else (compile, provided, runtime) is a
// plain dependency. Version text — including a ${property} reference — is
// kept verbatim; name matching carries the graph even where the version is
// indirected.
func parseMaven(rel string, data []byte) (Manifest, error) {
	var raw mavenManifest
	if err := decodeXML(data, &raw); err != nil {
		return Manifest{}, err
	}
	group := raw.GroupID
	if group == "" {
		group = raw.Parent.GroupID
	}
	version := raw.Version
	if version == "" {
		version = raw.Parent.Version
	}
	m := Manifest{
		Path:      rel,
		Ecosystem: EcosystemMaven,
		Name:      mavenCoord(group, raw.ArtifactID),
		Version:   version,
		Root:      isRoot(rel),
	}
	for _, d := range raw.Dependencies {
		kind := KindDependencies
		switch {
		case strings.EqualFold(d.Scope, "test"):
			kind = KindDevDependencies
		case strings.EqualFold(d.Optional, "true"):
			kind = KindOptionalDependencies
		}
		m.Deps = append(m.Deps, DeclaredDep{
			Name:  mavenCoord(d.GroupID, d.ArtifactID),
			Range: d.Version,
			Kind:  kind,
		})
	}
	sortDeps(m.Deps)
	return m, nil
}

// mavenCoord joins group and artifact into the canonical coordinate; a
// missing group leaves the artifact alone rather than a leading colon.
func mavenCoord(group, artifact string) string {
	if artifact == "" {
		return ""
	}
	if group == "" {
		return artifact
	}
	return group + ":" + artifact
}
