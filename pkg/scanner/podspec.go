package scanner

import "strings"

// parsePodspec reads a CocoaPods podspec: the library's own name and version
// plus the pods it depends on. It is the library-side counterpart of the
// Podfile (full identity where the Podfile has none) and the closest iOS
// analogue of a package.json.
//
// The block parameter is matched by shape rather than by name, because
// `Pod::Spec.new do |s|` is a convention and a subspec's block introduces
// another one (`|ss|`); platform-scoped declarations (`s.ios.dependency`) are
// read the same way. A version assigned from a constant rather than a literal
// (`s.version = Acme::VERSION`) yields no version: there is nothing here to
// evaluate it against.
//
// Subspec dependencies are collected alongside the top-level ones rather than
// attributed to their subspec: the ecosystem-neutral shape has no place to put
// that distinction, and a dependency is a dependency for graph purposes.
func parsePodspec(rel string, data []byte) (Manifest, error) {
	m := Manifest{Path: rel, Ecosystem: EcosystemCocoaPods, Root: isRoot(rel)}
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
			// The outermost spec is read first, so its identity wins over any
			// subspec that assigns the same attribute later.
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
		if dep, ok := podspecDependency(line); ok {
			m.Deps = append(m.Deps, dep)
		}
	}
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}

// podspecDependency reads one `s.dependency 'Name', '~> 1.0'` line. The
// version requirement is optional, and several may follow the name as separate
// arguments.
func podspecDependency(line string) (DeclaredDep, bool) {
	args, ok := rubyDottedCall(line, "dependency")
	if !ok {
		return DeclaredDep{}, false
	}
	return rubyDeclaration(line, args, KindDependencies)
}
