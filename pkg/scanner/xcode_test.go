package scanner

import (
	"reflect"
	"testing"
)

// pbxproj is a trimmed but structurally faithful Xcode project file: the
// settings repeat per build configuration, values appear both quoted and bare,
// and container openers sit alongside them.
const pbxproj = `// !$*UTF8*$!
{
	archiveVersion = 1;
	objectVersion = 56;
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
				MARKETING_VERSION = 1.2.3;
				PRODUCT_BUNDLE_IDENTIFIER = "com.acme.app";
			};
			name = Release;
		};
/* End XCBuildConfiguration section */
	};
}
`

func TestXcodeProjReadsBuildSettings(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Acme.xcodeproj/project.pbxproj", pbxproj)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "Acme.xcodeproj/project.pbxproj", Ecosystem: EcosystemXcode,
		Name: "com.acme.app", Version: "1.2.3", BuildNumber: "42",
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestXcodeProjFirstConfigurationWins(t *testing.T) {
	// Configurations that disagree are a real state; reporting the first is
	// enough to place the project, and the writer is the half that reconciles.
	dir := t.TempDir()
	write(t, dir, "Acme.xcodeproj/project.pbxproj", `{
	buildSettings = {
		MARKETING_VERSION = 1.0.0;
	};
	buildSettings = {
		MARKETING_VERSION = 2.0.0;
	};
}
`)
	if m := scanOne(t, dir); m.Version != "1.0.0" {
		t.Errorf("version = %q, want the first configuration's 1.0.0", m.Version)
	}
}

func TestXcodeProjSkipsContainersAndConditionalSettings(t *testing.T) {
	// A container opener has no terminating semicolon, and a conditional
	// assignment's bracket is not a name byte; neither may be misread as a
	// plain setting.
	dir := t.TempDir()
	write(t, dir, "Acme.xcodeproj/project.pbxproj", `{
	buildSettings = {
		MARKETING_VERSION[sdk=iphoneos*] = 9.9.9;
		MARKETING_VERSION = 1.0.0;
	};
}
`)
	if m := scanOne(t, dir); m.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", m.Version)
	}
}

func TestXcodeProjBuildSettingIdentifierIsNotAName(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Acme.xcodeproj/project.pbxproj", `{
	buildSettings = {
		PRODUCT_BUNDLE_IDENTIFIER = "$(INHERITED)";
		MARKETING_VERSION = 1.0.0;
	};
}
`)
	if m := scanOne(t, dir); m.Name != "" {
		t.Errorf("a build-setting reference must not become a name, got %q", m.Name)
	}
}

func TestScanSkipsAppleAndAndroidBuildFolders(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Info.plist", `<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.acme.app</string></dict></plist>`)
	// All of these hold copied or generated manifests describing something
	// other than the workspace.
	write(t, dir, "Pods/Alamofire/Info.plist", `<plist version="1.0"><dict/></plist>`)
	write(t, dir, "Carthage/Checkouts/dep/Info.plist", `<plist version="1.0"><dict/></plist>`)
	write(t, dir, "DerivedData/Build/Info.plist", `<plist version="1.0"><dict/></plist>`)
	write(t, dir, "Acme.xcodeproj/xcuserdata/me.xcuserdatad/Info.plist", `<plist version="1.0"><dict/></plist>`)
	write(t, dir, "app/build/intermediates/AndroidManifest.xml", `<manifest package="generated"/>`)
	write(t, dir, ".gradle/Info.plist", `<plist version="1.0"><dict/></plist>`)

	m := scanOne(t, dir)
	if m.Name != "com.acme.app" {
		t.Errorf("only the workspace's own manifest should be visible, got %+v", m)
	}
}
