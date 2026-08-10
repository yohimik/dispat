package writer

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
)

// The byte scanners get tested directly as well as through the writers. A
// whole-file test proves the common path; these prove the edges that a
// realistic manifest never reaches but a malformed one does.

func TestTOMLKeyValueForms(t *testing.T) {
	for _, tc := range []struct {
		line, key string
		ok        bool
	}{
		{`serde = "1.0"`, "serde", true},
		{`  spaced   =   "1.0"`, "spaced", true},
		{`"quoted" = "1.0"`, "quoted", true},
		{`'literal' = "1.0"`, "literal", true},
		// An '=' inside a string is not the separator.
		{`k = "a = b"`, "k", true},
		{`no separator here`, "", false},
		{`= "1.0"`, "", false},
		{``, "", false},
		// The '=' sits inside the quoted key, so there is no separator at the
		// top level and the line declares nothing this writer can splice.
		{`"quoted = key" 1.0`, "", false},
	} {
		key, after, ok := tomlKeyValue(tc.line)
		if ok != tc.ok || key != tc.key {
			t.Errorf("tomlKeyValue(%q) = %q,%v, want %q,%v", tc.line, key, ok, tc.key, tc.ok)
		}
		if ok && (after <= 0 || after > len(tc.line)) {
			t.Errorf("tomlKeyValue(%q) offset %d out of range", tc.line, after)
		}
	}
}

func TestStripTOMLCommentIgnoresQuotedHash(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{`k = "v" # comment`, `k = "v" `},
		{`k = "a # b"`, `k = "a # b"`},
		{`k = 'a # b'`, `k = 'a # b'`},
		{`k = "esc \" # still in"`, `k = "esc \" # still in"`},
		{`# whole line`, ``},
		{`k = "v"`, `k = "v"`},
	} {
		if got := stripTOMLComment(tc.line); got != tc.want {
			t.Errorf("stripTOMLComment(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestTOMLQuotedSpanEdges(t *testing.T) {
	for _, tc := range []struct {
		line string
		from int
		want string
		ok   bool
	}{
		{`k = "1.0"`, 3, "1.0", true},
		{`k = '1.0'`, 3, "1.0", true},
		{`k = 1`, 3, "", false},
		{`k = "unterminated`, 3, "", false},
		// A multi-line literal does not hold its content on this line.
		{`k = """1.0`, 3, "", false},
		{`k = '''1.0`, 3, "", false},
		{`k =`, 3, "", false},
	} {
		start, end, ok := tomlQuotedSpan(tc.line, tc.from)
		if ok != tc.ok {
			t.Errorf("tomlQuotedSpan(%q) ok = %v, want %v", tc.line, ok, tc.ok)
			continue
		}
		if ok && tc.line[start:end] != tc.want {
			t.Errorf("tomlQuotedSpan(%q) = %q, want %q", tc.line, tc.line[start:end], tc.want)
		}
	}
}

func TestTOMLInlineValueSpanDistinguishesDottedKeys(t *testing.T) {
	line := `x = { module = "g:a", version.ref = "kotlin" }`
	if _, _, ok := tomlInlineValueSpan(line, 4, "version"); ok {
		t.Error("version.ref must not match a bare version key")
	}
	line = `x = { module = "g:a", version = "1.0" }`
	start, end, ok := tomlInlineValueSpan(line, 4, "version")
	if !ok || line[start:end] != "1.0" {
		t.Errorf("inline version not found in %q", line)
	}
	// A key that only appears as a substring of another must not match.
	if _, _, ok := tomlInlineValueSpan(`x = { myversion = "1.0" }`, 4, "version"); ok {
		t.Error("myversion must not match version")
	}
}

func TestGradleIdentBeforeNamesTheBlock(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{`android {`, "android"},
		{`    defaultConfig {`, "defaultConfig"},
		{`implementation (project(path: ':x')) {`, "implementation"},
		{`if (enabled) {`, "if"},
		{`} else {`, "else"},
		{`{`, ""},
	} {
		masked, _ := gradleMask(tc.line, false)
		i := strings.LastIndexByte(masked, '{')
		if got := gradleIdentBefore(tc.line, masked, i); got != tc.want {
			t.Errorf("gradleIdentBefore(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestIsGradleConfiguration(t *testing.T) {
	for _, name := range []string{
		"implementation", "api", "runtimeOnly", "compileOnly", "testImplementation",
		"androidTestImplementation", "debugImplementation", "kapt", "ksp",
		"annotationProcessor", "releaseApi",
	} {
		if !isGradleConfiguration(name) {
			t.Errorf("%s should be a dependency configuration", name)
		}
	}
	for _, name := range []string{"", "classpath", "exclude", "project", "println"} {
		if isGradleConfiguration(name) {
			t.Errorf("%s should not be a dependency configuration", name)
		}
	}
}

func TestPlistNextValueSkipsEveryNonStringType(t *testing.T) {
	// A key whose value is a boolean, an integer, an array or a dictionary is
	// not a splice target, and the walk must step over it whole.
	src := `<plist version="1.0"><dict>
  <key>b</key><true/>
  <key>i</key><integer>3</integer>
  <key>a</key><array><string>inner</string></array>
  <key>d</key><dict><key>CFBundleShortVersionString</key><string>9.9</string></dict>
  <key>CFBundleShortVersionString</key><string>1.0</string>
</dict></plist>`
	s, text, found, err := plistVersionSpan([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !found || text != "1.0" {
		t.Fatalf("found=%v text=%q, want the root dictionary's 1.0", found, text)
	}
	if src[s.start:s.end] != "1.0" {
		t.Errorf("span covers %q", src[s.start:s.end])
	}
}

func TestPlistVersionSpanMalformedInput(t *testing.T) {
	for _, src := range []string{
		`<plist><dict><key>CFBundleShortVersionString</key>`, // dangling key
		`<plist><dict>`,             // unterminated
		`<plist><array/></plist>`,   // no dictionary
		``,                          // empty
		`<plist><dict><key>a</key>`, // dangling key, not the wanted one
		`not xml at all`,            // no elements
	} {
		if _, _, found, _ := plistVersionSpan([]byte(src)); found {
			t.Errorf("%q should not yield a span", src)
		}
	}
}

func TestMavenDependencyVersionPartialElements(t *testing.T) {
	// Real poms carry dependencies with no version, with a property version,
	// and with elements the reader does not care about.
	src := `<project><dependencies>
  <dependency><groupId>g</groupId><artifactId>a</artifactId></dependency>
  <dependency><groupId>g</groupId><artifactId>b</artifactId><version>${p}</version></dependency>
  <dependency><groupId>g</groupId><artifactId>c</artifactId><version>1.0</version><scope>test</scope><exclusions><exclusion><groupId>x</groupId></exclusion></exclusions></dependency>
</dependencies></project>`
	spans, declared, _, err := mavenSpans([]byte(src), map[string]int{"g:a": 0, "g:b": 1, "g:c": 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"g:a", "g:b", "g:c"} {
		if !declared[name] {
			t.Errorf("%s should be declared", name)
		}
	}
	if _, ok := spans[0]; ok {
		t.Error("a dependency with no version has no span")
	}
	if _, ok := spans[1]; ok {
		t.Error("a property version has no span")
	}
	if s, ok := spans[2]; !ok || src[s.start:s.end] != "1.0" {
		t.Error("the literal version was not located past its sibling elements")
	}
}

func TestAtomicWriteRenameFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission checks are meaningless as root")
	}
	// Renaming onto a non-empty directory fails, which exercises the last
	// error path without needing to break the filesystem.
	dir := t.TempDir()
	target := filepath.Join(dir, "package.json")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "occupant"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(target, []byte("{}")); err == nil {
		t.Error("renaming onto a non-empty directory must fail")
	}
	// The temp file must not be left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("a failed write left %d entries behind", len(entries))
	}
}

func TestGradleMaskBlanksWhatItMust(t *testing.T) {
	for _, tc := range []struct {
		line          string
		in, wantOut   bool
		wantNoBraceAt string
	}{
		{`implementation "a{b}c"`, false, false, "{"},
		{`// commented { brace`, false, false, "{"},
		{`/* opens`, false, true, ""},
		{`still inside { brace`, true, true, "{"},
		{`closes */ real {`, true, false, ""},
		{`implementation 'esc \' still {'`, false, false, "{"},
		{`implementation "unterminated {`, false, false, "{"},
	} {
		masked, out := gradleMask(tc.line, tc.in)
		if len(masked) != len(tc.line) {
			t.Fatalf("gradleMask(%q) changed the length", tc.line)
		}
		if out != tc.wantOut {
			t.Errorf("gradleMask(%q, %v) comment state = %v, want %v", tc.line, tc.in, out, tc.wantOut)
		}
		if tc.wantNoBraceAt != "" && strings.Contains(masked, tc.wantNoBraceAt) {
			t.Errorf("gradleMask(%q) left %q visible: %q", tc.line, tc.wantNoBraceAt, masked)
		}
	}
	// A brace outside any literal stays visible, or scope tracking would break.
	if masked, _ := gradleMask(`android {`, false); !strings.Contains(masked, "{") {
		t.Error("a real brace must survive masking")
	}
}

func TestAttrValueSpanEdges(t *testing.T) {
	for _, tc := range []struct {
		window, attr, want string
		ok                 bool
	}{
		{`<m android:versionName="1.0">`, "versionName", "1.0", true},
		{`<m versionName='1.0'>`, "versionName", "1.0", true},
		{`<m versionName = "1.0">`, "versionName", "1.0", true},
		// A longer name that merely ends with the one sought must not match.
		{`<m myversionName="1.0">`, "versionName", "", false},
		{`<m versionName>`, "versionName", "", false},
		{`<m versionName="unterminated>`, "versionName", "", false},
		{`<m other="1.0">`, "versionName", "", false},
		{`<m versionName=1.0>`, "versionName", "", false},
	} {
		s, ok := attrValueSpan([]byte(tc.window), 0, tc.attr)
		if ok != tc.ok {
			t.Errorf("attrValueSpan(%q) ok = %v, want %v", tc.window, ok, tc.ok)
			continue
		}
		if ok && tc.window[s.start:s.end] != tc.want {
			t.Errorf("attrValueSpan(%q) = %q, want %q", tc.window, tc.window[s.start:s.end], tc.want)
		}
	}
}

func TestMavenCoordJoins(t *testing.T) {
	for _, tc := range []struct{ group, artifact, want string }{
		{"g", "a", "g:a"},
		{"", "a", "a"},
		{"g", "", ""},
		{"", "", ""},
		{"  g  ", "  a  ", "g:a"},
	} {
		if got := mavenCoord(tc.group, tc.artifact); got != tc.want {
			t.Errorf("mavenCoord(%q,%q) = %q, want %q", tc.group, tc.artifact, got, tc.want)
		}
	}
}

func TestPBXVerifyCatchesDamage(t *testing.T) {
	before := "{\n\tMARKETING_VERSION = 1.0;\n}\n"
	// The happy path: one assignment, one value, balance intact.
	if err := pbxVerify([]byte(before), []byte("{\n\tMARKETING_VERSION = 2.0;\n}\n"), "2.0", 1); err != nil {
		t.Errorf("a clean rewrite must verify: %v", err)
	}
	for _, tc := range []struct {
		name, after, version string
		count                int
	}{
		{"brace lost", "{\n\tMARKETING_VERSION = 2.0;\n", "2.0", 1},
		{"wrong value", "{\n\tMARKETING_VERSION = 9.9;\n}\n", "2.0", 1},
		{"assignment lost", "{\n}\n", "2.0", 1},
		{"assignment gained", "{\n\tMARKETING_VERSION = 2.0;\n\tMARKETING_VERSION = 2.0;\n}\n", "2.0", 1},
	} {
		if err := pbxVerify([]byte(before), []byte(tc.after), tc.version, tc.count); err == nil {
			t.Errorf("%s should fail verification", tc.name)
		}
	}
}

func TestXMLHelpersOnAbsentAndNestedContent(t *testing.T) {
	if got := xmlAttr(startElement(t, `<x a="1"/>`), "missing"); got != "" {
		t.Errorf("absent attribute = %q, want empty", got)
	}
	if xmlHasAttr(startElement(t, `<x a="1"/>`), "missing") {
		t.Error("absent attribute reported present")
	}
	// An element holding another element is not a text value.
	dec, start := decoderAt(t, `<v><nested/>text</v>`)
	_ = start
	_, _, spliceable, err := xmlElementTextSpan(dec, []byte(`<v><nested/>text</v>`))
	if err != nil {
		t.Fatal(err)
	}
	if spliceable {
		t.Error("an element containing another element is not spliceable")
	}
}

// startElement decodes the first start element of a fragment.
func startElement(t *testing.T, src string) xml.StartElement {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(src))
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("no start element in %q: %v", src, err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se
		}
	}
}

// decoderAt returns a decoder positioned just past the fragment's first start
// element, which is what xmlElementTextSpan expects.
func decoderAt(t *testing.T, src string) (*xml.Decoder, xml.StartElement) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(src))
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("no start element in %q: %v", src, err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			return dec, se
		}
	}
}

func TestPyKindTables(t *testing.T) {
	for _, tc := range []struct {
		table, key string
		want       manifest.Kind
		ok         bool
	}{
		{"project", "dependencies", manifest.KindDependencies, true},
		{"project.optional-dependencies", "cli", manifest.KindOptionalDependencies, true},
		{"dependency-groups", "dev", manifest.KindDevDependencies, true},
		{"project", "classifiers", "", false},
		{"tool.black", "anything", "", false},
	} {
		got, ok := pyArrayKind(tc.table, tc.key)
		if ok != tc.ok || got != tc.want {
			t.Errorf("pyArrayKind(%q,%q) = %q,%v", tc.table, tc.key, got, ok)
		}
	}
	for _, tc := range []struct {
		table string
		want  manifest.Kind
		ok    bool
	}{
		{"tool.poetry.dependencies", manifest.KindDependencies, true},
		{"tool.poetry.dev-dependencies", manifest.KindDevDependencies, true},
		{"tool.poetry.group.test.dependencies", manifest.KindDevDependencies, true},
		{"tool.poetry.group.test.other", "", false},
		{"tool.poetry", "", false},
		{"project", "", false},
	} {
		got, ok := pyTableKind(tc.table)
		if ok != tc.ok || got != tc.want {
			t.Errorf("pyTableKind(%q) = %q,%v", tc.table, got, ok)
		}
	}
}

func TestGradleVerifyCatchesDamage(t *testing.T) {
	before := "android {\n defaultConfig {\n versionName \"1.0\"\n }\n}\ndependencies {\n implementation 'g:a:1'\n}\n"
	after := strings.NewReplacer("\"1.0\"", "\"2.0\"", "'g:a:1'", "'g:a:2'").Replace(before)
	applied := []Edit{{Name: "g:a", Range: "2"}}
	if err := gradleVerify(before, after, applied, "2.0", true); err != nil {
		t.Errorf("a clean rewrite must verify: %v", err)
	}
	for _, tc := range []struct{ name, after string }{
		{"brace lost", strings.Replace(after, "}\n", "\n", 1)},
		{"dependency lost", strings.Replace(after, " implementation 'g:a:2'\n", "", 1)},
		{"wrong dependency text", strings.Replace(after, "'g:a:2'", "'g:a:9'", 1)},
		{"version lost", strings.Replace(after, " versionName \"2.0\"\n", "", 1)},
		{"wrong version text", strings.Replace(after, "\"2.0\"", "\"9.9\"", 1)},
	} {
		if err := gradleVerify(before, tc.after, applied, "2.0", true); err == nil {
			t.Errorf("%s should fail verification", tc.name)
		}
	}
}

func TestXMLWellFormedRejectsBrokenDocuments(t *testing.T) {
	for _, src := range []string{`<a>`, `<a></b>`, `<a attr=>`, `<`} {
		if err := xmlWellFormed([]byte(src)); err == nil {
			t.Errorf("%q should not be well formed", src)
		}
	}
	for _, src := range []string{`<a/>`, `<a>text</a>`, ``, `<?xml version="1.0"?><a/>`} {
		if err := xmlWellFormed([]byte(src)); err != nil {
			t.Errorf("%q should be well formed: %v", src, err)
		}
	}
}

func TestRewriteErrorsLeaveTheFileAlone(t *testing.T) {
	// Every writer that parses before splicing must fail loudly on a broken
	// document and leave it exactly as it found it.
	for _, tc := range []struct{ name, src string }{
		{"Info.plist", `<plist><dict><key>a</key><string>1</dict>`},
		{"AndroidManifest.xml", `<manifest android:versionName="1.0">`},
		{"pom.xml", `<project><version>1.0</version>`},
		{"Acme.nuspec", `<package><metadata><version>1.0</version>`},
		{"Acme.csproj", `<Project><PropertyGroup><Version>1.0</Version>`},
		{"libs.versions.toml", "[versions\nbroken"},
		{"Cargo.toml", "[package\nbroken"},
		{"pyproject.toml", "[project\nbroken"},
	} {
		path := seed(t, tc.name, tc.src)
		_, err := Rewrite(path, "2.0.0", []Edit{{Name: "g:a", Range: "2.0"}})
		if err == nil {
			t.Errorf("%s: a broken document should fail", tc.name)
		}
		if read(t, path) != tc.src {
			t.Errorf("%s: a failed rewrite modified the file", tc.name)
		}
	}
}

func TestMavenSpansStopsOnTruncatedDependency(t *testing.T) {
	if _, _, _, err := mavenSpans([]byte(`<project><dependencies><dependency><groupId>g</groupId>`), nil); err == nil {
		t.Error("a truncated dependency element should fail")
	}
	if _, _, _, err := mavenSpans([]byte(`<project><version>1.0`), nil); err == nil {
		t.Error("a truncated version element should fail")
	}
}

func TestRubyStripCommentAndQuoted(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{`pod 'A' # comment`, `pod 'A' `},
		{`pod 'A#notacomment'`, `pod 'A#notacomment'`},
		{`pod "A#{x}"`, `pod "A#{x}"`},
		{`pod 'esc \' # inside'`, `pod 'esc \' # inside'`},
		{`pod "esc \" # inside"`, `pod "esc \" # inside"`},
		{`# whole line`, ``},
	} {
		if got := rubyStripComment(tc.line); got != tc.want {
			t.Errorf("rubyStripComment(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
	for _, tc := range []struct {
		line string
		from int
		want string
		ok   bool
	}{
		{`pod 'A'`, 3, "A", true},
		{`pod "A"`, 3, "A", true},
		{`pod   'A'`, 3, "A", true},
		{`pod A`, 3, "", false},
		{`pod 'unterminated`, 3, "", false},
		{`pod`, 3, "", false},
		{`pod '\''`, 3, `\'`, true},
	} {
		start, end, ok := rubyQuoted(tc.line, tc.from)
		if ok != tc.ok {
			t.Errorf("rubyQuoted(%q) ok = %v, want %v", tc.line, ok, tc.ok)
			continue
		}
		if ok && tc.line[start:end] != tc.want {
			t.Errorf("rubyQuoted(%q) = %q, want %q", tc.line, tc.line[start:end], tc.want)
		}
	}
}

func TestVerifyRubyPodsCatchesDamage(t *testing.T) {
	call := func(line string) (int, bool) { return rubyBareCall(line, "pod") }
	good := "pod 'A', '2.0'\n"
	applied := []Edit{{Name: "A", Range: "2.0"}}
	if err := verifyRubyPods(good, call, applied, "", false); err != nil {
		t.Errorf("a clean rewrite must verify: %v", err)
	}
	if err := verifyRubyPods("pod 'A', '9.9'\n", call, applied, "", false); err == nil {
		t.Error("a wrong requirement should fail verification")
	}
	if err := verifyRubyPods("", call, applied, "", false); err == nil {
		t.Error("a lost declaration should fail verification")
	}
	// The own-version half.
	spec := "s.version = '2.0'\n"
	if err := verifyRubyPods(spec, call, nil, "2.0", true); err != nil {
		t.Errorf("a clean version rewrite must verify: %v", err)
	}
	if err := verifyRubyPods("s.version = '9.9'\n", call, nil, "2.0", true); err == nil {
		t.Error("a wrong version should fail verification")
	}
	if err := verifyRubyPods("", call, nil, "2.0", true); err == nil {
		t.Error("a lost version assignment should fail verification")
	}
}

func TestCatalogSlotForShapes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		value      any
		coordinate string
		ok         bool
	}{
		{"shorthand", "g:a:1.0", "g:a", true},
		{"shorthand without version", "g:a", "", false},
		{"module and ref", map[string]any{"module": "g:a", "version": map[string]any{"ref": "v"}}, "g:a", true},
		{"group and name", map[string]any{"group": "g", "name": "a", "version": "1.0"}, "g:a", true},
		{"module without colon", map[string]any{"module": "ga"}, "", false},
		{"no coordinate", map[string]any{"version": "1.0"}, "", false},
		{"no version", map[string]any{"module": "g:a"}, "", false},
		{"wrong type", 42, "", false},
	} {
		coordinate, _, ok := catalogSlotFor("alias", tc.value)
		if ok != tc.ok || coordinate != tc.coordinate {
			t.Errorf("%s: = %q,%v, want %q,%v", tc.name, coordinate, ok, tc.coordinate, tc.ok)
		}
	}
}

func TestCargoDependencyNameShapes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		value    any
		want     string
		writable bool
	}{
		{"plain", "1.0", "serde", true},
		{"table", map[string]any{"version": "1.0"}, "serde", true},
		{"renamed", map[string]any{"package": "real", "version": "1.0"}, "real", true},
		{"workspace", map[string]any{"workspace": true}, "serde", false},
		{"path only", map[string]any{"path": "../x"}, "serde", false},
		{"wrong type", 42, "", false},
	} {
		got, writable := cargoDependencyName("serde", tc.value)
		if got != tc.want || writable != tc.writable {
			t.Errorf("%s: = %q,%v, want %q,%v", tc.name, got, writable, tc.want, tc.writable)
		}
	}
}

func TestPyBracketDelta(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{
		{`dependencies = [`, 1},
		{`]`, -1},
		{`["a", "b"]`, 0},
		{`["a[b]"]`, 0},
		{`"[["`, 0},
		{`'[['`, 0},
		{`"esc \" ["`, 0},
		{``, 0},
	} {
		if got := pyBracketDelta(tc.body); got != tc.want {
			t.Errorf("pyBracketDelta(%q) = %d, want %d", tc.body, got, tc.want)
		}
	}
}
