package scanner

import (
	"reflect"
	"testing"
)

func TestAndroidManifestLegacyAttributes(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "AndroidManifest.xml", `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.acme.app"
    android:versionCode="42"
    android:versionName="1.2.3">
    <application android:label="Acme" />
</manifest>
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "AndroidManifest.xml", Ecosystem: EcosystemAndroid,
		Name: "com.acme.app", Version: "1.2.3", BuildNumber: "42", Root: true,
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestAndroidManifestNamespacedProjectReadsEmpty(t *testing.T) {
	// A modern Android Gradle Plugin project declares its namespace and both
	// versions in build.gradle and none of them here. Reading nothing is a
	// correct reading of a healthy file.
	dir := t.TempDir()
	write(t, dir, "AndroidManifest.xml", `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <application
        android:allowBackup="true"
        android:label="@string/app_name" />
</manifest>
`)
	m := scanOne(t, dir)
	want := Manifest{Path: "AndroidManifest.xml", Ecosystem: EcosystemAndroid, Root: true}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestAndroidManifestRejectsForeignRootElement(t *testing.T) {
	// The file name is an exact match, so a document that is not a <manifest>
	// is a broken file — and an error is the only way to tell it apart from a
	// modern project that legitimately declares nothing.
	dir := t.TempDir()
	write(t, dir, "AndroidManifest.xml", `<?xml version="1.0"?><resources><string name="x">y</string></resources>`)
	if _, err := New().Scan(t.Context(), dir); err == nil {
		t.Error("a non-manifest document must surface in the joined error")
	}
}
