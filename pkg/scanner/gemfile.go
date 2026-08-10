package scanner

import "strings"

// parseGemfile reads a Bundler Gemfile: the gems an application depends on. A
// Gemfile declares no identity of its own (it is a consumption manifest, the
// same shape as a pip requirements file or a Podfile) so only its dependencies
// are read.
//
// Gems are attributed to the nearest preceding `group ... do` line, and a
// development or test group contributes development dependencies; the inline
// `group:` option is honoured too, since both spellings are ordinary. As in
// the Podfile parser there is deliberately no `do`/`end` nesting counter: a
// Gemfile is executable Ruby that branches on the environment, and a counter
// tracking its block structure would be confidently wrong on ordinary files.
func parseGemfile(rel string, data []byte) (Manifest, error) {
	m := Manifest{Path: rel, Ecosystem: EcosystemRubyGems, Root: isRoot(rel)}
	kind := KindDependencies
	for _, raw := range strings.Split(string(data), "\n") {
		line := rubyStripComment(raw)
		if dev, ok := gemfileGroup(line); ok {
			kind = KindDependencies
			if dev {
				kind = KindDevDependencies
			}
			continue
		}
		dep, ok := gemDeclaration(line, kind)
		if !ok {
			continue
		}
		if isRubyDevGroup(line) {
			dep.Kind = KindDevDependencies
		}
		m.Deps = append(m.Deps, dep)
	}
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}

// gemDeclaration reads one `gem 'name'` line. A gem pinned to a git revision
// has no version text at all; its name still carries the graph.
func gemDeclaration(line string, kind Kind) (DeclaredDep, bool) {
	args, ok := rubyBareCall(line, "gem")
	if !ok {
		return DeclaredDep{}, false
	}
	return rubyDeclaration(line, args, kind)
}

// gemfileGroup matches `group :development, :test do`, reporting whether the
// group it opens is a development one.
func gemfileGroup(line string) (dev, ok bool) {
	args, ok := rubyBareCall(line, "group")
	if !ok || !strings.HasSuffix(strings.TrimSpace(line), "do") {
		return false, false
	}
	return isRubyDevGroup(line[args:]), true
}

// isRubyDevGroup reports a Bundler group naming development or test. Groups are
// symbols rather than strings, so they are matched as written; a gem in any
// other group (`:production`, a deployment group) is a plain dependency.
func isRubyDevGroup(text string) bool {
	return strings.Contains(text, ":development") || strings.Contains(text, ":test")
}
