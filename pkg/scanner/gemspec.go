package scanner

import "strings"

// gemspecDependencyMethods are the three ways a gemspec declares a dependency,
// and the kind each carries. add_dependency and add_runtime_dependency are the
// same thing under two names; only the development form is separate.
var gemspecDependencyMethods = []struct {
	method string
	kind   Kind
}{
	{"add_development_dependency", KindDevDependencies},
	{"add_runtime_dependency", KindDependencies},
	{"add_dependency", KindDependencies},
}

// parseGemspec reads a RubyGems gemspec: the gem's own name and version plus
// the gems it depends on. It is the library-side counterpart of the Gemfile —
// full identity where the Gemfile has none — and the Ruby analogue of a
// package.json.
//
// The block parameter is matched by shape rather than by name, because
// `Gem::Specification.new do |spec|` is a convention rather than a rule. Nearly
// every published gem assigns its version from a constant
// (`spec.version = Acme::VERSION`) so that the library and its packaging agree;
// that is not a literal and yields no version, which is the honest answer —
// the number lives in a Ruby source file this package does not evaluate.
func parseGemspec(rel string, data []byte) (Manifest, error) {
	m := Manifest{Path: rel, Ecosystem: EcosystemRubyGems, Root: isRoot(rel)}
	for _, raw := range strings.Split(string(data), "\n") {
		line := rubyStripComment(raw)
		if attr, valueStart, ok := rubyAttrAssignment(line); ok {
			start, end, quoted := rubyQuoted(line, valueStart)
			if !quoted {
				continue
			}
			value := line[start:end]
			if !isRubyLiteral(value) {
				continue
			}
			switch attr {
			case "name":
				if m.Name == "" {
					m.Name = value
				}
			case "version":
				if m.Version == "" {
					m.Version = value
				}
			}
			continue
		}
		if dep, ok := gemspecDependency(line); ok {
			m.Deps = append(m.Deps, dep)
		}
	}
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}

// gemspecDependency reads one `spec.add_dependency "name", "~> 1.0"` line in
// any of its three spellings.
func gemspecDependency(line string) (DeclaredDep, bool) {
	for _, m := range gemspecDependencyMethods {
		if args, ok := rubyDottedCall(line, m.method); ok {
			return rubyDeclaration(line, args, m.kind)
		}
	}
	return DeclaredDep{}, false
}
