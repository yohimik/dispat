package scanner

import (
	"reflect"
	"testing"
)

func TestPlistIdentityVersionAndBuildNumber(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Info.plist", `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleIdentifier</key>
	<string>com.acme.app</string>
	<key>CFBundleShortVersionString</key>
	<string>1.2.3</string>
	<key>CFBundleVersion</key>
	<string>42</string>
	<key>LSRequiresIPhoneOS</key>
	<true/>
</dict>
</plist>
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "Info.plist", Ecosystem: EcosystemPlist,
		Name: "com.acme.app", Version: "1.2.3", BuildNumber: "42", Root: true,
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestPlistIgnoresNestedDictKeys(t *testing.T) {
	// A real Info.plist nests dictionaries and arrays carrying keys of their
	// own; only the root dictionary may answer.
	dir := t.TempDir()
	write(t, dir, "Info.plist", `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleURLTypes</key>
	<array>
		<dict>
			<key>CFBundleVersion</key>
			<string>999</string>
			<key>CFBundleShortVersionString</key>
			<string>9.9.9</string>
		</dict>
	</array>
	<key>UIApplicationSceneManifest</key>
	<dict>
		<key>CFBundleIdentifier</key>
		<string>com.acme.decoy</string>
	</dict>
	<key>CFBundleIdentifier</key>
	<string>com.acme.app</string>
	<key>CFBundleShortVersionString</key>
	<string>1.2.3</string>
	<key>CFBundleVersion</key>
	<string>42</string>
</dict>
</plist>
`)
	m := scanOne(t, dir)
	if m.Name != "com.acme.app" || m.Version != "1.2.3" || m.BuildNumber != "42" {
		t.Errorf("nested keys leaked into the result: %+v", m)
	}
}

func TestPlistBuildSettingIdentifierIsNotAName(t *testing.T) {
	// Every Xcode project spells this identifier identically, so keeping it
	// would have NameIndex report the shared literal as an ambiguous name
	// across otherwise unrelated apps. The version is kept verbatim, matching
	// how the Maven parser keeps a ${property}.
	dir := t.TempDir()
	write(t, dir, "Info.plist", `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>$(PRODUCT_BUNDLE_IDENTIFIER)</string>
	<key>CFBundleShortVersionString</key>
	<string>$(MARKETING_VERSION)</string>
</dict>
</plist>
`)
	m := scanOne(t, dir)
	if m.Name != "" {
		t.Errorf("a build-setting reference must not become a name, got %q", m.Name)
	}
	if m.Version != "$(MARKETING_VERSION)" {
		t.Errorf("version = %q, want the reference kept verbatim", m.Version)
	}
}

func TestPlistWithoutRootDictionaryReadsEmpty(t *testing.T) {
	// A well-formed plist that declares nothing is a valid answer, not a
	// malformed file.
	dir := t.TempDir()
	write(t, dir, "Info.plist", `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<array>
	<string>nothing to declare</string>
</array>
</plist>
`)
	m := scanOne(t, dir)
	if m.Name != "" || m.Version != "" || m.BuildNumber != "" {
		t.Errorf("an array-rooted plist declares nothing: %+v", m)
	}
}
