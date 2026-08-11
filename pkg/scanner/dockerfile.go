package scanner

import (
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// parseDockerfile reads a Dockerfile's image references. A Dockerfile declares
// no identity of its own — the name and version of what it builds live in the
// build command, not the file — so it contributes dependencies only.
//
// Finding the references is manifest.DockerfileRefs' job, shared with the
// writer so a base image the reader counts as a dependency is one the writer
// can always find again to reconcile.
//
// An interpolated or digest-pinned reference is reported as it stands rather
// than dropped. The edge is real either way, and letting the writer decline
// the rewrite for a stated reason is more use than the dependency quietly
// disappearing here.
func parseDockerfile(rel string, data []byte) (Manifest, error) {
	m := Manifest{
		Path:      rel,
		Ecosystem: EcosystemDocker,
		Root:      isRoot(rel),
	}
	for _, ref := range manifest.DockerfileRefs(strings.Split(string(data), "\n")) {
		parsed := manifest.ParseImageRef(ref.Text)
		m.Deps = append(m.Deps, DeclaredDep{
			Name:  parsed.Repository,
			Range: parsed.Tag,
			Kind:  KindDependencies,
		})
	}
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}
