package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
)

// buildCounterNames is one file name per format that carries a build counter,
// mirroring what the scanner reads a BuildNumber from. The fence below keeps
// buildWriters level with it, so reading and writing counters cannot drift.
var buildCounterNames = []string{
	"Info.plist", "AndroidManifest.xml", "project.pbxproj", "build.gradle", "pubspec.yaml",
}

func TestEveryBuildCounterFormatHasAWriter(t *testing.T) {
	covered := map[manifest.Format]bool{}
	for _, name := range buildCounterNames {
		f, ok := manifest.FormatOf(name)
		if !ok {
			t.Fatalf("%s names no format", name)
		}
		covered[f] = true
		if _, ok := buildWriters[f]; !ok {
			t.Errorf("the scanner reads a build number from %s but SetBuild cannot write one", name)
		}
	}
	for f := range buildWriters {
		if !covered[f] {
			t.Errorf("format %q has a build writer the scanner reads no counter from", f)
		}
	}
}

func TestSetBuildWritesEachCounter(t *testing.T) {
	cases := map[string]struct{ file, src, build, want string }{
		"plist": {
			"Info.plist",
			"<plist><dict>\n<key>CFBundleShortVersionString</key><string>1.2.0</string>\n<key>CFBundleVersion</key><string>41</string>\n</dict></plist>",
			"42",
			"<key>CFBundleVersion</key><string>42</string>",
		},
		"android": {
			"AndroidManifest.xml",
			`<manifest package="com.acme.app" android:versionName="1.2.0" android:versionCode="41"/>`,
			"42",
			`android:versionCode="42"`,
		},
		"gradle groovy": {
			"build.gradle",
			"android {\n  defaultConfig {\n    versionCode 41\n    versionName \"1.2.0\"\n  }\n}\n",
			"42",
			"versionCode 42",
		},
		"gradle kotlin": {
			"build.gradle.kts",
			"android {\n  defaultConfig {\n    versionCode = 41\n    versionName = \"1.2.0\"\n  }\n}\n",
			"42",
			"versionCode = 42",
		},
		"pubspec replaces the suffix": {
			"pubspec.yaml",
			"name: acme\nversion: 1.2.0+41\n",
			"42",
			"version: 1.2.0+42",
		},
		"pubspec appends one": {
			"pubspec.yaml",
			"name: acme\nversion: 1.2.0\n",
			"42",
			"version: 1.2.0+42",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := seed(t, tc.file, tc.src)
			res, err := SetBuild(path, tc.build)
			if err != nil {
				t.Fatal(err)
			}
			if !res.BuildWritten {
				t.Fatalf("BuildWritten = false: %+v", res)
			}
			if got := read(t, path); !strings.Contains(got, tc.want) {
				t.Errorf("file missing %q:\n%s", tc.want, got)
			}
		})
	}
}

func TestSetBuildWritesEveryXcodeConfiguration(t *testing.T) {
	src := "{\n\tbuildSettings = {\n\t\tCURRENT_PROJECT_VERSION = 41;\n\t\tMARKETING_VERSION = 1.2.0;\n\t};\n\tbuildSettings = {\n\t\tCURRENT_PROJECT_VERSION = 41;\n\t};\n}\n"
	path := seed(t, "project.pbxproj", src)
	res, err := SetBuild(path, "42")
	if err != nil {
		t.Fatal(err)
	}
	if !res.BuildWritten {
		t.Fatalf("BuildWritten = false: %+v", res)
	}
	got := read(t, path)
	if strings.Count(got, "CURRENT_PROJECT_VERSION = 42;") != 2 {
		t.Errorf("both configurations must move together:\n%s", got)
	}
	if !strings.Contains(got, "MARKETING_VERSION = 1.2.0;") {
		t.Errorf("the marketing version is not SetBuild's to touch:\n%s", got)
	}
}

func TestSetBuildLeavesWhatItCannotClaim(t *testing.T) {
	// An absent counter is a project's decision; a deferred plist value is an
	// indirection to keep; an already-correct counter is no change. All three
	// leave the file byte-identical with BuildWritten false.
	cases := map[string]struct{ file, src string }{
		"absent gradle counter": {"build.gradle", "android {\n  defaultConfig {\n    versionName \"1.0\"\n  }\n}\n"},
		"deferred plist value":  {"Info.plist", "<plist><dict><key>CFBundleVersion</key><string>$(CURRENT_PROJECT_VERSION)</string></dict></plist>"},
		"already correct":       {"AndroidManifest.xml", `<manifest android:versionCode="42"/>`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := seed(t, tc.file, tc.src)
			res, err := SetBuild(path, "42")
			if err != nil {
				t.Fatal(err)
			}
			if res.BuildWritten {
				t.Errorf("BuildWritten = true: %+v", res)
			}
			if got := read(t, path); got != tc.src {
				t.Errorf("the file changed:\n%s", got)
			}
		})
	}
}

func TestSetBuildRefusals(t *testing.T) {
	// A word where Android and Gradle demand an integer, a manifest whose
	// format has no counter, and a name no format claims.
	for _, file := range []string{"AndroidManifest.xml", "build.gradle"} {
		path := seed(t, file, "placeholder")
		if _, err := SetBuild(path, "banana"); err == nil {
			t.Errorf("%s: a non-integer counter must be refused", file)
		}
	}
	path := seed(t, "package.json", `{"name":"acme"}`)
	if _, err := SetBuild(path, "42"); !errors.Is(err, ErrNoBuildCounter) {
		t.Errorf("package.json: got %v, want ErrNoBuildCounter", err)
	}
	path = seed(t, "notes.txt", "nothing")
	if _, err := SetBuild(path, "42"); !errors.Is(err, ErrUnsupportedManifest) {
		t.Errorf("notes.txt: got %v, want ErrUnsupportedManifest", err)
	}
	if _, err := SetBuild(path, ""); err == nil {
		t.Error("an empty build value must be refused")
	}
}
