package scanner

import "strings"

// parsePodfile reads a CocoaPods Podfile: the pods an application depends on.
// A Podfile declares no identity of its own — it is a consumption manifest, the
// same shape as a pip requirements file — so only its dependencies are read.
//
// Pods are attributed to the nearest preceding `target '...' do` line, and a
// target whose name ends in "Tests" contributes development dependencies.
// Deliberately no `do`/`end` nesting counter: real Podfiles branch on the
// environment, define methods and shell out, so a counter tracking Ruby's
// block structure would be confidently wrong on ordinary files. Getting a kind
// slightly wrong is a much smaller error than mis-tracking scope.
func parsePodfile(rel string, data []byte) (Manifest, error) {
	m := Manifest{Path: rel, Ecosystem: EcosystemCocoaPods, Root: isRoot(rel)}
	kind := KindDependencies
	for _, raw := range strings.Split(string(data), "\n") {
		line := rubyStripComment(raw)
		if name, ok := podfileTarget(line); ok {
			kind = KindDependencies
			if isTestTargetName(name) {
				kind = KindDevDependencies
			}
			continue
		}
		if dep, ok := podDeclaration(line, kind); ok {
			m.Deps = append(m.Deps, dep)
		}
	}
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}

// podfileTarget matches `target 'Name' do`, reporting the target's name.
func podfileTarget(line string) (string, bool) {
	args, ok := rubyBareCall(line, "target")
	if !ok || !strings.HasSuffix(strings.TrimSpace(line), "do") {
		return "", false
	}
	start, end, ok := rubyQuoted(line, args)
	if !ok {
		return "", false
	}
	return line[start:end], true
}

// isTestTargetName reports the CocoaPods naming convention for a test target:
// AcmeTests, AcmeUITests, AcmeSnapshotTests.
func isTestTargetName(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), "tests")
}

// podDeclaration reads one `pod 'Name'` line, with its optional version
// requirements and options. A pod pinned to a git revision or a podspec has no
// version text at all; its name still carries the graph.
func podDeclaration(line string, kind Kind) (DeclaredDep, bool) {
	args, ok := rubyBareCall(line, "pod")
	if !ok {
		return DeclaredDep{}, false
	}
	start, end, ok := rubyQuoted(line, args)
	if !ok {
		return DeclaredDep{}, false
	}
	dep := DeclaredDep{Name: line[start:end], Kind: kind}
	if dep.Name == "" {
		return DeclaredDep{}, false
	}

	// Any number of version requirements may follow the name, each its own
	// argument: `pod 'X', '>= 1.0', '< 2.0'`. They are one constraint, so they
	// are reported as one range.
	var requirements []string
	for i := end + 1; i < len(line); {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) || line[i] != ',' {
			break
		}
		start, end, ok := rubyQuoted(line, i+1)
		if !ok {
			break // an option hash, not another requirement
		}
		requirements = append(requirements, line[start:end])
		i = end + 1
	}
	if text := strings.Join(requirements, ", "); isRubyLiteral(text) {
		dep.Range = text
	}

	// A local pod points at a folder in the same repository — the strongest
	// workspace signal a Podfile carries.
	if start, end, ok := rubyOption(line, "path"); ok {
		if path := line[start:end]; isRubyLiteral(path) {
			dep.LocalPath = path
		}
	}
	return dep, true
}
