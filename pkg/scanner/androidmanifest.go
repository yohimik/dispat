package scanner

import (
	"encoding/xml"
)

// androidManifest is the subset of an AndroidManifest.xml the scanner reads.
// The attribute tags carry no namespace, so they match whether or not the file
// spells them with the conventional `android:` prefix, Go resolves the prefix
// to its URL and an untagged field matches any namespace.
type androidManifest struct {
	// XMLName pins the root element: an exact file name makes a false positive
	// all but impossible, so a document that is not a <manifest> is a broken file
	// worth an error rather than a silent empty read, which is otherwise
	// indistinguishable from a legitimately modern project.
	XMLName     xml.Name `xml:"manifest"`
	Package     string   `xml:"package,attr"`
	VersionName string   `xml:"versionName,attr"`
	VersionCode string   `xml:"versionCode,attr"`
}

// parseAndroidManifest reads an AndroidManifest.xml: the application ID as the
// package name, android:versionName as the version and android:versionCode as
// the build number. The manifest declares no dependencies (those live in the
// Gradle build script) so this is an identity-only manifest feeding versioning
// rather than the dependency graph.
//
// All three attributes are the pre-namespacing shape. A project on a modern
// Android Gradle Plugin declares its namespace and both versions in
// build.gradle instead and leaves none of them here, so an empty result is a
// correct reading of a healthy file, not a failure.
func parseAndroidManifest(rel string, data []byte) (Manifest, error) {
	var raw androidManifest
	if err := decodeXML(data, &raw); err != nil {
		return Manifest{}, err
	}
	return Manifest{
		Path:        rel,
		Ecosystem:   EcosystemAndroid,
		Name:        raw.Package,
		Version:     raw.VersionName,
		BuildNumber: raw.VersionCode,
		Root:        isRoot(rel),
	}, nil
}
