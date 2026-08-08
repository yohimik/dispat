package scanner

import (
	"encoding/xml"
	"strings"
)

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
	if err := xml.Unmarshal(data, &raw); err != nil {
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
