package scanner

import (
	"testing"
)

// Fuzzing the hand-rolled parsers: none of them may panic on any input, and
// the library-backed ones ride along for free through parserFor. Malformed
// input must come back as an error (or a well-formed partial Manifest), never
// as a crash — a scanner walks whatever a checkout contains.

// fuzzManifestNames drives every registered parser: exact names, a suffix
// name and a pattern name.
var fuzzManifestNames = []string{
	"package.json", "go.mod", "Cargo.toml", "pyproject.toml",
	"composer.json", "pom.xml", "pubspec.yaml", "App.csproj",
	"requirements.txt", "requirements-dev.txt",
	"Info.plist", "AndroidManifest.xml", "libs.versions.toml",
	"project.pbxproj", "Podfile", "Acme.podspec",
	"build.gradle", "build.gradle.kts",
}

func FuzzParsers(f *testing.F) {
	seeds := []string{
		"",
		"{",
		`{"name": "a", "dependencies": {"b": "^1.0.0"}}`,
		"module example.com/m\n\ngo 1.25.0\nrequire example.com/dep v1.0.0\n",
		"[package]\nname = \"a\"\nversion = \"1.0.0\"\n[dependencies]\nserde = { version = \"1\", package = 42 }\n",
		"[project]\nname = \"a\"\ndependencies = [\"b>=1\"]\n",
		"<Project><PropertyGroup><PackageId>A</PackageId></PropertyGroup></Project>",
		"<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><project><artifactId>a</artifactId></project>",
		"name: app\nversion: 1.0\ndependencies:\n  http: ^1.0.0\n",
		"pkg==1.0 \\\n  --hash=sha256:abc\n-e ./local\n",
		"\xff\xfe\x00broken",
		"<plist version=\"1.0\"><dict><key>CFBundleVersion</key><string>1</string></dict></plist>",
		"<plist><dict><key>a</key><array><dict><key>b</key><string>c</string></dict></array></dict></plist>",
		"<manifest package=\"com.acme\" android:versionName=\"1.0\" android:versionCode=\"1\"/>",
		"[versions]\na = \"1\"\n[libraries]\nb = { module = \"g:a\", version.ref = \"a\" }\n",
		"{ buildSettings = {\nMARKETING_VERSION = 1.0;\nPRODUCT_BUNDLE_IDENTIFIER = \"a\";\n}; }\n",
		"target 'A' do\n  pod 'B', '~> 1.0'\n  pod 'C', :path => '../c'\nend\n",
		"Pod::Spec.new do |s|\n  s.name = 'A'\n  s.version = '1.0'\n  s.dependency 'B', '~> 1'\nend\n",
		"android {\n defaultConfig {\n versionName \"1.0\"\n }\n}\ndependencies {\n implementation 'g:a:1'\n}\n",
		"dependencies {\n implementation(project(\":core\"))\n /* unterminated",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data string) {
		for _, name := range fuzzManifestNames {
			parse, ok := parserFor(name)
			if !ok {
				t.Fatalf("no parser for %q", name)
			}
			m, err := parse(name, []byte(data))
			if err != nil {
				continue
			}
			for _, d := range m.Deps {
				_ = d.Kind.String()
			}
		}
	})
}

func FuzzPep508Dep(f *testing.F) {
	for _, s := range []string{
		"requests>=2.0,<3", "Acme_Core==1.0.0", "uvicorn[standard]>=0.30",
		"bare-name", "a;python_version<'3'", "x @ https://example.com/x.whl", "==", "[",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		d := pep508Dep(line, KindDependencies)
		_ = d.Name
	})
}

// The hand-rolled byte scanners get their own targets: FuzzParsers exercises
// them through whole files, but a helper fed a deliberately awkward single
// line is where an off-by-one slice actually shows up.

func FuzzPodDeclaration(f *testing.F) {
	for _, s := range []string{
		"pod 'A', '~> 1.0'", `pod "A",  "1.0"`, "pod 'A', :path => '../a'",
		"pod 'A', path: '../a'", "pod 'A', '>= 1', '< 2'", "pod 'A'", "pod",
		"pod '", "pod ''", "  pod 'A' # comment", "podium 'A'", "pod 'A', '#{x}'",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		dep, _ := podDeclaration(rubyStripComment(line), KindDependencies)
		_ = dep.Name
	})
}

func FuzzPodspecStatement(f *testing.F) {
	for _, s := range []string{
		"s.name = 'A'", "s.version  = '1.0'", "s.version = A::VERSION",
		"ss.ios.dependency 'A/B', '~> 1'", "s.source = { :git => 'x', :tag => s.version }",
		"s.dependency", "s.", ".name = 'a'", "a.b.c.d.name = 'x'", "s.name == 'a'",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		line = rubyStripComment(line)
		if attr, start, ok := rubyAttrAssignment(line); ok {
			_ = attr
			_, _, _ = rubyQuoted(line, start)
		}
		dep, _ := podspecDependency(line)
		_ = dep.Name
	})
}

func FuzzPBXSetting(f *testing.F) {
	for _, s := range []string{
		"MARKETING_VERSION = 1.0;", `PRODUCT_BUNDLE_IDENTIFIER = "com.a.b";`,
		"buildSettings = {", "A = ;", `A = "unterminated`, "A=", "= 1;",
		"MARKETING_VERSION[sdk=iphoneos*] = 1.0;", "\tA\t=\t1\t;",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		key, value, span, ok := pbxSetting(line)
		if !ok {
			return
		}
		// A reported span must address the value it reported.
		if span[0] < 0 || span[1] > len(line) || span[0] > span[1] {
			t.Fatalf("span %v out of range for %q", span, line)
		}
		if line[span[0]:span[1]] != value {
			t.Fatalf("span %v does not cover %q in %q (key %q)", span, value, line, key)
		}
	})
}

func FuzzGradleLine(f *testing.F) {
	for _, s := range []string{
		"implementation 'g:a:1.0'", `implementation("g:a:1.0")`, "implementation(libs.x)",
		"implementation project(':core')", "compileOnly project(path: ':a:b')",
		"versionName \"1.0\"", "versionName = \"1.0\"", "versionCode 42",
		"implementation \"g:a:$v\"", "implementation '", "implementation()", "}", "{",
		"/* open", "// comment", "implementation (project(path:':x')) {",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		masked, _ := gradleMask(line, false)
		if len(masked) != len(line) {
			t.Fatalf("mask changed the length of %q", line)
		}
		_ = gradleUpdateScope(nil, line, masked)
		if dep, ok := gradleDependency(line, masked); ok {
			_ = dep.Name
		}
		for _, name := range []string{"versionName", "versionCode", "applicationId", "namespace"} {
			_, _ = gradleProperty(line, masked, name)
		}
	})
}
