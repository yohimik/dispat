package writer

import (
	"os"
	"strings"
	"testing"
)

func TestAndroidManifestRewritePreservesEveryOtherByte(t *testing.T) {
	// The word "versionName" also appears in a comment and as an attribute of
	// a nested element; neither may be touched, because the search is confined
	// to the root element's own start tag.
	src := `<?xml version="1.0" encoding="utf-8"?>
<!-- android:versionName="0.0.0" is set below -->
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.acme.app"
    android:versionCode="42"
    android:versionName="1.0.0" >
    <application android:label="Acme">
        <meta-data android:name="versionName" android:value="1.0.0" />
    </application>
</manifest>
`
	path := seed(t, "AndroidManifest.xml", src)

	res, err := Rewrite(path, "2.0.0", []Edit{{Name: "ghost", Range: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(src, `android:versionName="1.0.0" >`, `android:versionName="2.0.0" >`, 1)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if !res.VersionWritten || len(res.Applied) != 0 || len(res.Missing) != 1 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestAndroidManifestRewriteNoChangeLeavesFileAlone(t *testing.T) {
	src := "<manifest xmlns:android=\"http://schemas.android.com/apk/res/android\" android:versionName=\"1.0.0\" />\n"
	path := seed(t, "AndroidManifest.xml", src)
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

func TestAndroidManifestRewriteNamespacedProjectLeavesFileAlone(t *testing.T) {
	// A modern Gradle project keeps the version in build.gradle. There is
	// nothing to write, and inventing the attribute here would add one the
	// build ignores.
	src := "<manifest xmlns:android=\"http://schemas.android.com/apk/res/android\">\n    <application />\n</manifest>\n"
	path := seed(t, "AndroidManifest.xml", src)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten || read(t, path) != src {
		t.Errorf("an absent version attribute has nothing to write: %+v", res)
	}
}

func TestAndroidManifestRewriteSingleQuotedValueAndEscaping(t *testing.T) {
	src := "<manifest package='com.acme.app' android:versionName='1.0.0' />\n"
	path := seed(t, "AndroidManifest.xml", src)
	if _, err := Rewrite(path, "2.0.0", nil); err != nil {
		t.Fatal(err)
	}
	if got, want := read(t, path), "<manifest package='com.acme.app' android:versionName='2.0.0' />\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// A quote in the replacement must be escaped, never spliced raw into an
	// attribute value.
	path = seed(t, "AndroidManifest.xml", "<manifest android:versionName=\"1.0.0\" />\n")
	if _, err := Rewrite(path, `2.0.0"evil`, nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); strings.Contains(got, `"evil`) {
		t.Errorf("quote not escaped: %q", got)
	}
	if err := xmlWellFormed([]byte(read(t, path))); err != nil {
		t.Errorf("rewrite produced invalid XML: %v", err)
	}
}
