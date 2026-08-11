package manifest

import "testing"

func TestComposeIdentity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		services  []ComposeService
		repo, tag string
	}{
		{
			name: "a service that builds and tags wins over a commoner image",
			services: []ComposeService{
				{Name: "cache", Image: "redis:7.2"},
				{Name: "replica", Image: "redis:7.2"},
				{Name: "api", Image: "ghcr.io/acme/api:1.4.2", Builds: true},
			},
			repo: "ghcr.io/acme/api", tag: "1.4.2",
		},
		{
			name: "two builders are separated by service name, not map order",
			services: []ComposeService{
				{Name: "worker", Image: "ghcr.io/acme/worker:2.0.0", Builds: true},
				{Name: "api", Image: "ghcr.io/acme/api:1.4.2", Builds: true},
			},
			repo: "ghcr.io/acme/api", tag: "1.4.2",
		},
		{
			name: "a builder with no tag cannot be the identity",
			services: []ComposeService{
				{Name: "api", Image: "ghcr.io/acme/api", Builds: true},
				{Name: "cache", Image: "redis:7.2"},
			},
			repo: "redis", tag: "7.2",
		},
		{
			name: "with nothing building, the commonest tagged image wins",
			services: []ComposeService{
				{Name: "cache", Image: "redis:7.2"},
				{Name: "web", Image: "ghcr.io/acme/api:2.0.0"},
				{Name: "worker", Image: "ghcr.io/acme/api:2.0.0"},
			},
			repo: "ghcr.io/acme/api", tag: "2.0.0",
		},
		{
			name: "an equal count goes to the lowest service name",
			services: []ComposeService{
				{Name: "zeta", Image: "ghcr.io/acme/z:1.0"},
				{Name: "alpha", Image: "ghcr.io/acme/a:1.0"},
			},
			repo: "ghcr.io/acme/a", tag: "1.0",
		},
		{
			name: "an interpolated image is never an identity",
			services: []ComposeService{
				{Name: "api", Image: "${REGISTRY}/api:${TAG}", Builds: true},
				{Name: "cache", Image: "redis:7.2"},
			},
			repo: "redis", tag: "7.2",
		},
		{
			name: "no tagged image at all means no identity",
			services: []ComposeService{
				{Name: "cache", Image: "redis"},
				{Name: "db", Image: "postgres"},
			},
		},
		{
			name: "a digest-pinned builder still names the file",
			services: []ComposeService{
				{Name: "api", Image: "ghcr.io/acme/api:1.0.0@sha256:abc", Builds: true},
			},
			repo: "ghcr.io/acme/api", tag: "1.0.0",
		},
		{name: "no services at all"},
	} {
		repo, tag := ComposeIdentity(tc.services)
		if repo != tc.repo || tag != tc.tag {
			t.Errorf("%s: ComposeIdentity = %q@%q, want %q@%q", tc.name, repo, tag, tc.repo, tc.tag)
		}
	}
}

func TestComposeIdentityIgnoresInputOrder(t *testing.T) {
	// A YAML mapping decodes in whatever order the decoder feels like, so the
	// answer has to come from the data. Every rotation of the same services
	// must agree, and none of them may be reordered in place.
	services := []ComposeService{
		{Name: "cache", Image: "redis:7.2"},
		{Name: "replica", Image: "redis:7.2"},
		{Name: "api", Image: "ghcr.io/acme/api:1.4.2", Builds: true},
		{Name: "db", Image: "postgres:16.1"},
	}
	for i := range services {
		rotated := append(append([]ComposeService{}, services[i:]...), services[:i]...)
		before := rotated[0].Name
		repo, tag := ComposeIdentity(rotated)
		if repo != "ghcr.io/acme/api" || tag != "1.4.2" {
			t.Errorf("rotation %d: ComposeIdentity = %q@%q", i, repo, tag)
		}
		if rotated[0].Name != before {
			t.Errorf("rotation %d: the caller's slice was reordered", i)
		}
	}
}
