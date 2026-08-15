package scanner

import (
	"sort"

	"github.com/yohimik/dispat/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// composeFile is the sliver of a compose file the scanner reads. Everything is
// typed `any` on purpose: a compose file is full of values whose YAML type
// varies by author — `build` is a folder string or a mapping, `environment` is
// a mapping or a list, `ports` is a list of strings that look like image
// references — and a strict struct would fail the whole manifest over any one
// of them.
type composeFile struct {
	Services map[string]any `yaml:"services"`
}

// parseCompose reads a Docker Compose file: the image the folder builds
// becomes the manifest's own identity, and every other service's image becomes
// a dependency.
//
// The identity rule is shared with the writer through
// manifest.ComposeIdentity, so the version this reports is read from the same
// service the writer would write it back to.
//
// A service's build.tags are the other names the built image answers to. They
// are outputs rather than inputs, so they are not dependencies — but they do
// carry the version, which is why the writer rewrites them.
func parseCompose(rel string, data []byte) (Manifest, error) {
	var raw composeFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		Path:      rel,
		Ecosystem: EcosystemDocker,
		Root:      isRoot(rel),
	}
	services := make([]manifest.ComposeService, 0, len(raw.Services))
	for name, value := range raw.Services {
		body, ok := value.(map[string]any)
		if !ok {
			// A null service, or one written as a list: a declared service
			// with nothing readable in it, said so rather than swallowed.
			m.Dropped = append(m.Dropped, "service "+name+": not a mapping")
			continue
		}
		image, _ := body["image"].(string)
		_, builds := body["build"]
		services = append(services, manifest.ComposeService{Name: name, Image: image, Builds: builds})
	}
	repository, tag := manifest.ComposeIdentity(services)
	m.Name, m.Version = repository, tag

	for _, s := range services {
		ref := manifest.ParseImageRef(s.Image)
		if s.Image == "" || ref.Repository == repository {
			continue // the file's own image is not a dependency on itself
		}
		m.Deps = append(m.Deps, DeclaredDep{
			Name:  ref.Repository,
			Range: ref.Tag,
			Kind:  KindDependencies,
		})
	}
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	sort.Strings(m.Dropped)
	return m, nil
}
