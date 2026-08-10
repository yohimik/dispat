package writer

import (
	"os"
	"strings"
	"testing"
)

// pbxproj is a trimmed but structurally faithful Xcode project file: the
// marketing version repeats per build configuration, one bare and one quoted.
const pbxproj = `// !$*UTF8*$!
{
	archiveVersion = 1;
	objects = {
/* Begin XCBuildConfiguration section */
		13B07F941A680F5B00A75B9A /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				CURRENT_PROJECT_VERSION = 42;
				MARKETING_VERSION = 1.2.3;
				PRODUCT_BUNDLE_IDENTIFIER = com.acme.app;
			};
			name = Debug;
		};
		13B07F951A680F5B00A75B9A /* Release */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				CURRENT_PROJECT_VERSION = 42;
				MARKETING_VERSION = "1.2.3";
			};
			name = Release;
		};
/* End XCBuildConfiguration section */
	};
}
`

func TestXcodeProjRewriteUpdatesEveryConfiguration(t *testing.T) {
	path := seed(t, "project.pbxproj", pbxproj)
	res, err := Rewrite(path, "2.0.0", []Edit{{Name: "ghost", Range: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		"MARKETING_VERSION = 1.2.3;", "MARKETING_VERSION = 2.0.0;",
		`MARKETING_VERSION = "1.2.3";`, `MARKETING_VERSION = "2.0.0";`,
	).Replace(pbxproj)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	// The quoting style of each assignment survives: the splice covers the
	// value, not the quotes around it.
	if got := read(t, path); !strings.Contains(got, `MARKETING_VERSION = "2.0.0";`) ||
		!strings.Contains(got, "MARKETING_VERSION = 2.0.0;") {
		t.Errorf("quoting style not preserved: %q", got)
	}
	if !res.VersionWritten || len(res.Missing) != 1 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestXcodeProjRewriteNoChangeLeavesFileAlone(t *testing.T) {
	src := "{\n\tbuildSettings = {\n\t\tMARKETING_VERSION = 1.0.0;\n\t};\n}\n"
	path := seed(t, "project.pbxproj", src)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Rewrite(path, "1.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten || read(t, path) != src || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("no-op rewrite touched the file: %+v", res)
	}
}

func TestXcodeProjRewriteRefusesStructuralBytes(t *testing.T) {
	// There is no grammar to re-parse a project file against, so anything that
	// could close or open a token is refused before it is ever spliced.
	src := "{\n\tbuildSettings = {\n\t\tMARKETING_VERSION = 1.0.0;\n\t};\n}\n"
	for _, version := range []string{`2.0.0";`, "2.0.0}", "2.0.0;", "2.0\n.0"} {
		path := seed(t, "project.pbxproj", src)
		if _, err := Rewrite(path, version, nil); err == nil {
			t.Errorf("version %q must be refused", version)
		}
		if read(t, path) != src {
			t.Errorf("a refused rewrite modified the file for %q", version)
		}
	}
}

func TestXcodeProjRewriteLeavesConditionalAndAbsentSettingsAlone(t *testing.T) {
	src := "{\n\tbuildSettings = {\n\t\tMARKETING_VERSION[sdk=iphoneos*] = 9.9.9;\n\t\tCURRENT_PROJECT_VERSION = 42;\n\t};\n}\n"
	path := seed(t, "project.pbxproj", src)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten || read(t, path) != src {
		t.Errorf("nothing writable here: %+v", res)
	}
}
