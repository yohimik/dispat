package scanner

import "strings"

// A Gradle build script is a Groovy or Kotlin program, so this parser does not
// try to understand it — it recognises the handful of statement shapes that
// declare an identity, a version or a dependency, scoped by brace nesting, and
// ignores everything else. A shape it does not recognise contributes nothing
// rather than a guess, which matters more here than anywhere else in the
// package: real Android projects compute their versionName from a properties
// file and name most dependencies through version-catalog accessors
// (`implementation libs.retrofit`) that this file cannot resolve.

// parseGradleBuild reads a build.gradle or build.gradle.kts. Both dialects are
// accepted, since they differ only in whether a property assignment carries an
// '=' and whether a call's arguments are parenthesised.
//
// Identity comes from applicationId, falling back to namespace, so an app
// module names itself the same way its AndroidManifest.xml does. versionName
// and versionCode are read only inside android { defaultConfig { … } }, which
// keeps a product flavour's override from being mistaken for the module's own.
//
// Dependencies are read only inside dependencies { … }, and never from
// buildscript { dependencies { … } }, whose classpath entries are build tooling
// rather than anything the module ships. A literal "group:artifact:version"
// coordinate is named by its Maven coordinate, the same spelling a pom.xml or a
// version catalog gives it. A project(':core') reference is recorded by its
// last path segment with no local path: a Gradle project path is relative to
// the build's root, which a single build file does not reveal, and guessing at
// the folder could resolve to a real but unrelated package.
func parseGradleBuild(rel string, data []byte) (Manifest, error) {
	m := Manifest{Path: rel, Ecosystem: EcosystemGradle, Root: isRoot(rel)}
	var (
		scope        []string
		namespace    string
		inComment    bool
		masked, line string
	)
	for _, raw := range strings.Split(string(data), "\n") {
		line = raw
		masked, inComment = gradleMask(raw, inComment)

		switch {
		case gradleScopeHas(scope, "defaultConfig"):
			if v, ok := gradleProperty(line, masked, "applicationId"); ok && m.Name == "" {
				m.Name = v
			}
			if v, ok := gradleProperty(line, masked, "versionName"); ok && m.Version == "" {
				m.Version = v
			}
			if v, ok := gradleProperty(line, masked, "versionCode"); ok && m.BuildNumber == "" {
				m.BuildNumber = v
			}
		case gradleScopeTop(scope) == "android":
			if v, ok := gradleProperty(line, masked, "namespace"); ok && namespace == "" {
				namespace = v
			}
		case gradleScopeHas(scope, "dependencies") && !gradleScopeHas(scope, "buildscript"):
			if dep, ok := gradleDependency(line, masked); ok {
				m.Deps = append(m.Deps, dep)
			}
		}
		scope = gradleUpdateScope(scope, line, masked)
	}
	if m.Name == "" {
		m.Name = namespace
	}
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}

// gradleMask blanks the contents of string literals and comments while keeping
// the line's length and its string delimiters, so brace counting and token
// scanning cannot be misled by punctuation inside a literal. The returned flag
// carries an unterminated block comment on to the next line.
func gradleMask(line string, inComment bool) (string, bool) {
	out := []byte(line)
	i := 0
	for i < len(line) {
		if inComment {
			if strings.HasPrefix(line[i:], "*/") {
				out[i], out[i+1] = ' ', ' '
				i += 2
				inComment = false
				continue
			}
			out[i] = ' '
			i++
			continue
		}
		switch {
		case strings.HasPrefix(line[i:], "//"):
			for ; i < len(line); i++ {
				out[i] = ' '
			}
		case strings.HasPrefix(line[i:], "/*"):
			out[i], out[i+1] = ' ', ' '
			i += 2
			inComment = true
		case line[i] == '\'' || line[i] == '"':
			quote := line[i]
			i++ // the delimiter itself stays visible
			for i < len(line) && line[i] != quote {
				if line[i] == '\\' && i+1 < len(line) {
					out[i], out[i+1] = ' ', ' '
					i += 2
					continue
				}
				out[i] = ' '
				i++
			}
			if i < len(line) {
				i++
			}
		default:
			i++
		}
	}
	return string(out), inComment
}

// gradleUpdateScope tracks the block nesting a line leaves behind, naming each
// block after the identifier that opens it.
func gradleUpdateScope(scope []string, line, masked string) []string {
	for i := 0; i < len(masked); i++ {
		switch masked[i] {
		case '{':
			scope = append(scope, gradleIdentBefore(line, masked, i))
		case '}':
			if len(scope) > 0 {
				scope = scope[:len(scope)-1]
			}
		}
	}
	return scope
}

// gradleIdentBefore names the block opening at i: the identifier preceding the
// brace, stepping over a parenthesised argument list so
// `implementation(project(":core")) {` is attributed to implementation rather
// than to whatever the arguments ended with.
func gradleIdentBefore(line, masked string, i int) string {
	i--
	for i >= 0 && (masked[i] == ' ' || masked[i] == '\t') {
		i--
	}
	if i >= 0 && masked[i] == ')' {
		depth := 0
		for ; i >= 0; i-- {
			if masked[i] == ')' {
				depth++
			}
			if masked[i] == '(' {
				if depth--; depth == 0 {
					break
				}
			}
		}
		i--
		for i >= 0 && (masked[i] == ' ' || masked[i] == '\t') {
			i--
		}
	}
	end := i + 1
	for i >= 0 && isGradleNameByte(masked[i]) {
		i--
	}
	if i+1 >= end {
		return ""
	}
	return line[i+1 : end]
}

// gradleScopeHas reports an enclosing block of the given name.
func gradleScopeHas(scope []string, name string) bool {
	for _, s := range scope {
		if s == name {
			return true
		}
	}
	return false
}

// gradleScopeTop names the innermost enclosing block.
func gradleScopeTop(scope []string) string {
	if len(scope) == 0 {
		return ""
	}
	return scope[len(scope)-1]
}

// gradleProperty matches `name "value"` and `name = "value"` — the Groovy and
// Kotlin spellings of one property assignment — and reports the literal it
// assigns. A value that is computed rather than written out (Gradle's own
// `versionName project.findProperty(…)`) has no literal and reports nothing.
func gradleProperty(line, masked, name string) (string, bool) {
	i := gradleSkipSpace(masked, 0)
	if !strings.HasPrefix(masked[i:], name) {
		return "", false
	}
	i += len(name)
	if i < len(masked) && isGradleNameByte(masked[i]) {
		return "", false // a longer identifier that merely starts with it
	}
	i = gradleSkipSpace(masked, i)
	if i < len(masked) && masked[i] == '=' {
		i = gradleSkipSpace(masked, i+1)
	}
	// versionCode is a bare integer rather than a string.
	if name == "versionCode" {
		start := i
		for i < len(masked) && masked[i] >= '0' && masked[i] <= '9' {
			i++
		}
		if i > start {
			return line[start:i], true
		}
	}
	start, end, ok := gradleQuoted(masked, i)
	if !ok {
		return "", false
	}
	value := line[start:end]
	if !isGradleLiteral(value) {
		return "", false
	}
	return value, true
}

// gradleDependency reads one dependency statement: a configuration name
// followed by a literal coordinate or a project reference, in either dialect's
// parenthesised or bare form.
func gradleDependency(line, masked string) (DeclaredDep, bool) {
	i := gradleSkipSpace(masked, 0)
	start := i
	for i < len(masked) && isGradleNameByte(masked[i]) {
		i++
	}
	kind, ok := gradleKind(line[start:i])
	if !ok {
		return DeclaredDep{}, false
	}
	i = gradleSkipSpace(masked, i)
	if i < len(masked) && masked[i] == '(' {
		i = gradleSkipSpace(masked, i+1)
	}

	if strings.HasPrefix(masked[i:], "project") {
		path, ok := gradleProjectPath(line, masked, i)
		if !ok {
			return DeclaredDep{}, false
		}
		segments := strings.Split(strings.Trim(path, ":"), ":")
		name := segments[len(segments)-1]
		if name == "" {
			return DeclaredDep{}, false
		}
		return DeclaredDep{Name: name, Kind: kind}, true
	}

	// Anything that is not a string literal here is a version-catalog
	// accessor or another expression, and naming it would be a guess.
	start, end, ok := gradleQuoted(masked, i)
	if !ok {
		return DeclaredDep{}, false
	}
	coordinate := line[start:end]
	if !isGradleLiteral(coordinate) {
		return DeclaredDep{}, false
	}
	return gradleCoordinateDep(coordinate, kind)
}

// gradleProjectPath reads the argument of a project(…) reference in both the
// positional and the named-argument spellings: project(':core') and
// project(path: ':core').
func gradleProjectPath(line, masked string, i int) (string, bool) {
	i += len("project")
	i = gradleSkipSpace(masked, i)
	if i >= len(masked) || masked[i] != '(' {
		return "", false
	}
	i = gradleSkipSpace(masked, i+1)
	if strings.HasPrefix(masked[i:], "path") {
		i = gradleSkipSpace(masked, i+len("path"))
		if i >= len(masked) || masked[i] != ':' {
			return "", false
		}
		i = gradleSkipSpace(masked, i+1)
	}
	start, end, ok := gradleQuoted(masked, i)
	if !ok {
		return "", false
	}
	path := line[start:end]
	if !isGradleLiteral(path) {
		return "", false
	}
	return path, true
}

// gradleKind maps a Gradle configuration onto the manifest vocabulary. The
// match is by shape rather than by an exhaustive list, because build variants
// and source sets multiply configurations without limit
// (debugImplementation, androidTestRuntimeOnly, freeReleaseApi).
//
// Test and debug-only configurations are development dependencies; so are the
// annotation processors, which run at build time and ship nothing. compileOnly
// is the closest thing Gradle has to a peer dependency: present while
// compiling, absent from what the consumer receives. Nothing here maps onto
// optionalDependencies, which is no loss — go.mod uses one kind of four.
func gradleKind(config string) (Kind, bool) {
	lower := strings.ToLower(config)
	switch {
	case lower == "":
		return "", false
	case strings.Contains(lower, "test"), strings.Contains(lower, "debug"):
		return KindDevDependencies, true
	case strings.Contains(lower, "annotationprocessor"), strings.Contains(lower, "kapt"),
		strings.Contains(lower, "ksp"):
		return KindDevDependencies, true
	case strings.Contains(lower, "compileonly"):
		return KindPeerDependencies, true
	case strings.HasSuffix(lower, "implementation"), strings.HasSuffix(lower, "api"),
		strings.HasSuffix(lower, "runtimeonly"):
		return KindDependencies, true
	}
	return "", false
}

// gradleQuoted measures the string literal at or after from in a masked line,
// returning the span its content occupies.
func gradleQuoted(masked string, from int) (start, end int, ok bool) {
	i := gradleSkipSpace(masked, from)
	if i >= len(masked) || (masked[i] != '\'' && masked[i] != '"') {
		return 0, 0, false
	}
	quote := masked[i]
	i++
	start = i
	for i < len(masked) && masked[i] != quote {
		i++
	}
	if i >= len(masked) {
		return 0, 0, false
	}
	return start, i, true
}

// isGradleLiteral reports a string whose text is what it says: no '$'
// interpolation naming a variable this package cannot evaluate.
func isGradleLiteral(value string) bool { return !strings.Contains(value, "$") }

// gradleSkipSpace advances past horizontal whitespace.
func gradleSkipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// isGradleNameByte reports a byte that may appear in a Gradle identifier.
func isGradleNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}
