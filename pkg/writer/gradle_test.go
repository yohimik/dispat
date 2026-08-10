package writer

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestGradleCatalogRewritePreservesEveryOtherByte(t *testing.T) {
	// Every version form a catalog can spell, plus comments, blank lines and
	// odd spacing. Only the four targeted literals may change.
	src := `# The project's dependency versions.
[versions]
retrofit = "2.9.0"
kotlin   = { require = "1.9.0" }

[libraries]
retrofit = { module = "com.squareup.retrofit2:retrofit", version.ref = "retrofit" }  # http
gson = { group = "com.google.code.gson", name = "gson", version = "2.10.1" }
junit = "junit:junit:4.13.2"
stdlib = { module = "org.jetbrains.kotlin:kotlin-stdlib", version.ref = "kotlin" }

[bundles]
networking = ["retrofit", "gson"]
`
	path := seed(t, "libs.versions.toml", src)

	res, err := Rewrite(path, "", []Edit{
		{Name: "com.squareup.retrofit2:retrofit", Range: "2.11.0"},   // through version.ref
		{Name: "com.google.code.gson:gson", Range: "2.11.0"},         // inline literal
		{Name: "junit:junit", Range: "4.13.3"},                       // shorthand segment
		{Name: "org.jetbrains.kotlin:kotlin-stdlib", Range: "2.0.0"}, // rich version behind a ref
		{Name: "com.acme:absent", Range: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`retrofit = "2.9.0"`, `retrofit = "2.11.0"`,
		`{ require = "1.9.0" }`, `{ require = "2.0.0" }`,
		`version = "2.10.1"`, `version = "2.11.0"`,
		`"junit:junit:4.13.2"`, `"junit:junit:4.13.3"`,
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if len(res.Applied) != 4 || len(res.Missing) != 1 || res.Missing[0].Name != "com.acme:absent" {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestGradleCatalogRewriteSharedRefFansOut(t *testing.T) {
	// Two libraries pinned to one [versions] entry cannot move independently.
	// That follows from the file the author wrote, so it is a contract rather
	// than a surprise — and both edits count as applied.
	src := `[versions]
kotlin = "1.9.0"

[libraries]
stdlib = { module = "org.jetbrains.kotlin:kotlin-stdlib", version.ref = "kotlin" }
reflect = { module = "org.jetbrains.kotlin:kotlin-reflect", version.ref = "kotlin" }
`
	path := seed(t, "libs.versions.toml", src)
	res, err := Rewrite(path, "", []Edit{
		{Name: "org.jetbrains.kotlin:kotlin-stdlib", Range: "2.0.0"},
		{Name: "org.jetbrains.kotlin:kotlin-reflect", Range: "2.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); !strings.Contains(got, `kotlin = "2.0.0"`) {
		t.Errorf("shared ref not written: %q", got)
	}
	if len(res.Applied) != 2 {
		t.Errorf("both edits land on the shared entry: %+v", res)
	}
}

func TestGradleCatalogRewriteConflictingEditsError(t *testing.T) {
	// A shared entry cannot be two versions at once; letting the last edit win
	// would silently move a library nobody asked to move.
	src := `[versions]
kotlin = "1.9.0"

[libraries]
stdlib = { module = "org.jetbrains.kotlin:kotlin-stdlib", version.ref = "kotlin" }
reflect = { module = "org.jetbrains.kotlin:kotlin-reflect", version.ref = "kotlin" }
`
	path := seed(t, "libs.versions.toml", src)
	_, err := Rewrite(path, "", []Edit{
		{Name: "org.jetbrains.kotlin:kotlin-stdlib", Range: "2.0.0"},
		{Name: "org.jetbrains.kotlin:kotlin-reflect", Range: "2.1.0"},
	})
	if !errors.Is(err, ErrConflictingEdits) {
		t.Errorf("got %v, want ErrConflictingEdits", err)
	}
	if read(t, path) != src {
		t.Error("a refused rewrite modified the file")
	}
}

func TestGradleCatalogRewriteNoChangeLeavesFileAlone(t *testing.T) {
	src := "[versions]\nkotlin = \"1.9.0\"\n\n[libraries]\nstdlib = { module = \"org.jetbrains.kotlin:kotlin-stdlib\", version.ref = \"kotlin\" }\n"
	path := seed(t, "libs.versions.toml", src)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Rewrite(path, "9.9.9", []Edit{{Name: "org.jetbrains.kotlin:kotlin-stdlib", Range: "1.9.0"}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A catalog declares no version of its own, so the version argument has no
	// target and cannot mark the file dirty.
	if len(res.Applied) != 0 || res.VersionWritten || read(t, path) != src || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("no-op rewrite touched the file: %+v", res)
	}
}

func TestGradleCatalogRewriteDeclinesWhatItCannotExpress(t *testing.T) {
	src := `[libraries]
bom = { module = "com.acme:bom" }
core = { module = "com.acme:core", version = "1.0.0" }
`
	path := seed(t, "libs.versions.toml", src)
	res, err := Rewrite(path, "", []Edit{
		{Name: "com.acme:bom", Range: "1.0.0"},                           // no version to replace
		{Name: "com.acme:core", Kind: "devDependencies", Range: "2.0.0"}, // a catalog has no kinds
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || len(res.Missing) != 2 || read(t, path) != src {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestGradleBuildRewritePreservesEveryOtherByte(t *testing.T) {
	// Both dialects' quote styles, a coordinate declared under two
	// configurations, and three shapes that carry no literal to replace.
	src := `android {
    defaultConfig {
        applicationId "com.acme.app"
        versionCode 42
        versionName "1.0.0"
    }
    productFlavors {
        free { versionName "1.0.0-free" }
    }
}

dependencies {
    implementation 'androidx.core:core-ktx:1.12.0'
    testImplementation "androidx.core:core-ktx:1.12.0"
    implementation(libs.retrofit)
    implementation "com.acme:iface:$ifaceVersion"
    implementation project(':core')
}
`
	path := seed(t, "build.gradle", src)

	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "androidx.core:core-ktx", Range: "1.13.0"},
		{Name: "com.acme:iface", Range: "9.9.9"}, // interpolated: no literal
		{Name: "com.acme:absent", Range: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`versionName "1.0.0"`, `versionName "2.0.0"`,
		`'androidx.core:core-ktx:1.12.0'`, `'androidx.core:core-ktx:1.13.0'`,
		`"androidx.core:core-ktx:1.12.0"`, `"androidx.core:core-ktx:1.13.0"`,
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	// A product flavour's versionName is not the module's own and must survive.
	if !strings.Contains(read(t, path), `versionName "1.0.0-free"`) {
		t.Error("a product flavour override was rewritten")
	}
	if !res.VersionWritten || len(res.Applied) != 1 || len(res.Missing) != 2 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestGradleBuildRewriteKotlinDialect(t *testing.T) {
	src := `android {
    defaultConfig {
        versionName = "1.0.0"
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.12.0")
}
`
	path := seed(t, "build.gradle.kts", src)
	res, err := Rewrite(path, "2.0.0", []Edit{{Name: "androidx.core:core-ktx", Range: "1.13.0"}})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`versionName = "1.0.0"`, `versionName = "2.0.0"`,
		`"androidx.core:core-ktx:1.12.0"`, `"androidx.core:core-ktx:1.13.0"`,
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if !res.VersionWritten || len(res.Applied) != 1 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestGradleBuildRewriteLeavesBuildscriptClasspathAlone(t *testing.T) {
	// A buildscript classpath is build tooling, not something the module ships.
	src := `buildscript {
    dependencies {
        classpath 'com.acme:plugin:1.0.0'
        implementation 'com.acme:core:1.0.0'
    }
}
`
	path := seed(t, "build.gradle", src)
	res, err := Rewrite(path, "", []Edit{{Name: "com.acme:core", Range: "2.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || read(t, path) != src {
		t.Errorf("buildscript dependencies must not be rewritten: %+v", res)
	}
}

func TestGradleBuildRewriteRefusesLiteralBreakingText(t *testing.T) {
	src := "android {\n    defaultConfig {\n        versionName \"1.0.0\"\n    }\n}\n"
	for _, bad := range []string{`2.0"`, `2.0'`, `2.0$x`, `2.0}`, "2.0\\"} {
		path := seed(t, "build.gradle", src)
		if _, err := Rewrite(path, bad, nil); err == nil {
			t.Errorf("version %q must be refused", bad)
		}
		if read(t, path) != src {
			t.Errorf("a refused rewrite modified the file for %q", bad)
		}
	}
}

func TestGradleBuildRewriteNoChangeLeavesFileAlone(t *testing.T) {
	src := "android {\n    defaultConfig {\n        versionName \"1.0.0\"\n    }\n}\n\ndependencies {\n    implementation 'com.acme:core:1.0.0'\n}\n"
	path := seed(t, "build.gradle", src)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Rewrite(path, "1.0.0", []Edit{{Name: "com.acme:core", Range: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || read(t, path) != src || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("no-op rewrite touched the file: %+v", res)
	}
}
