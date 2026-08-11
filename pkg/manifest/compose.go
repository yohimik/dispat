package manifest

import "sort"

// ComposeService is one Compose service reduced to the facts that decide which
// image a compose file is *about*: what it is called, what image it declares,
// and whether it builds that image here rather than pulling it.
//
// The type exists so the two halves of the tooling can share the decision
// without sharing the extraction. The reader gets these facts from a YAML
// decode; the writer gets them from the line walk it needs anyway to splice a
// scalar in place. Both then call ComposeIdentity, so they cannot disagree
// about which service owns the file's version — a disagreement that would show
// up as dispat writing a version into a service it never read one from.
type ComposeService struct {
	// Name is the service's key in the services map.
	Name string
	// Image is the declared image reference, empty when the service declares
	// none.
	Image string
	// Builds reports a service that declares a build section.
	Builds bool
}

// ComposeIdentity picks the image a compose file declares as its own, and
// returns its repository and tag. Both are empty when the file declares no
// identity, which is the honest answer for a compose file that only wires
// third-party services together.
//
// A compose file lists many images and says nothing about which one the
// folder ships, so the choice is made by two rules in order.
//
// First, the service that both builds and tags: a service with a build section
// and a tagged image is producing that image here, which is as close to "this
// is my package" as compose gets. Second, when nothing builds, the tagged
// repository the most services name — a scaled service appears several times
// under one image, and the third-party ones it sits beside appear once each.
//
// Ties in either rule go to the lowest service name. A YAML mapping has no
// order worth trusting, so the tie-break has to come from the data rather than
// from how it was decoded, or the answer would change between runs.
func ComposeIdentity(services []ComposeService) (repository, tag string) {
	sorted := make([]ComposeService, len(services))
	copy(sorted, services)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	refs := make([]ImageRef, len(sorted))
	for i, s := range sorted {
		refs[i] = ParseImageRef(s.Image)
	}
	// An interpolated reference names nothing a workspace can match, so it can
	// never be an identity however it was declared.
	eligible := func(i int) bool { return refs[i].HasTag() && !refs[i].Interpolated() }

	for i, s := range sorted {
		if s.Builds && eligible(i) {
			return refs[i].Repository, refs[i].Tag
		}
	}

	counts := make(map[string]int)
	for i := range sorted {
		if eligible(i) {
			counts[refs[i].Repository]++
		}
	}
	best := -1
	for i := range sorted {
		if eligible(i) && (best < 0 || counts[refs[i].Repository] > counts[refs[best].Repository]) {
			best = i
		}
	}
	if best < 0 {
		return "", ""
	}
	return refs[best].Repository, refs[best].Tag
}
