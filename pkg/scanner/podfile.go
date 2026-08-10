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

// podDeclaration reads one `pod 'Name'` line. A pod pinned to a git revision
// or a podspec has no version text at all; its name still carries the graph.
func podDeclaration(line string, kind Kind) (DeclaredDep, bool) {
	args, ok := rubyBareCall(line, "pod")
	if !ok {
		return DeclaredDep{}, false
	}
	return rubyDeclaration(line, args, kind)
}
