package writer

// The same fetched manifests the scanner tests read, kept here because the two
// packages are separate modules. Round-tripping real files is where a splice
// that looks right on a hand-written fixture turns out to be wrong.
//
//	React Native 0.76  github.com/react-native-community/template, 0.76-stable
//	Flutter add_to_app github.com/flutter/samples, main
//	tokio              github.com/tokio-rs/tokio, master
//	Rails              github.com/rails/rails, main

import (
	"strings"
	"testing"
)

const (
	rnInfoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleDisplayName</key>
	<string>Hello App Display Name</string>
	<key>CFBundleExecutable</key>
	<string>$(EXECUTABLE_NAME)</string>
	<key>CFBundleIdentifier</key>
	<string>$(PRODUCT_BUNDLE_IDENTIFIER)</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>$(PRODUCT_NAME)</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>$(MARKETING_VERSION)</string>
	<key>CFBundleSignature</key>
	<string>????</string>
	<key>CFBundleVersion</key>
	<string>$(CURRENT_PROJECT_VERSION)</string>
	<key>LSRequiresIPhoneOS</key>
	<true/>
	<key>NSAppTransportSecurity</key>
	<dict>
	  <!-- Do not change NSAllowsArbitraryLoads to true, or you will risk app rejection! -->
		<key>NSAllowsArbitraryLoads</key>
		<false/>
		<key>NSAllowsLocalNetworking</key>
		<true/>
	</dict>
	<key>NSLocationWhenInUseUsageDescription</key>
	<string></string>
	<key>UILaunchStoryboardName</key>
	<string>LaunchScreen</string>
	<key>UIRequiredDeviceCapabilities</key>
	<array>
		<string>arm64</string>
	</array>
	<key>UISupportedInterfaceOrientations</key>
	<array>
		<string>UIInterfaceOrientationPortrait</string>
		<string>UIInterfaceOrientationLandscapeLeft</string>
		<string>UIInterfaceOrientationLandscapeRight</string>
	</array>
	<key>UIViewControllerBasedStatusBarAppearance</key>
	<false/>
</dict>
</plist>
`

	flutterPubspec = `name: flutter_module_books
description: A Flutter module using the Pigeon package to demonstrate
  integrating Flutter in a realistic scenario where the existing platform app
  already has business logic and middleware constraints.
version: 1.0.0+1
resolution: workspace

environment:
  sdk: ^3.9.0-0

dependencies:
  flutter:
    sdk: flutter

dev_dependencies:
  analysis_defaults:
    path: ../../../analysis_defaults
  pigeon: ">=11.0.0 <27.0.0"
  flutter_test:
    sdk: flutter
`

	tokioCargoToml = `[package]
name = "tokio"
# When releasing to crates.io:
# - Remove path dependencies (if any)
# - Update doc url
#   - README.md
# - Update CHANGELOG.md.
# - Create "v1.x.y" git tag.
version = "1.53.1"
edition = "2021"
rust-version = "1.71"
authors = ["Tokio Contributors <team@tokio.rs>"]

[dependencies]
tokio-macros = { version = "~2.7.0", optional = true }

pin-project-lite = "0.2.11"

# Everything else is optional...
`

	railsGemfile = `# frozen_string_literal: true

source "https://rubygems.org"
gemspec

gem "minitest", "~> 6.0"
gem "minitest-mock"

gem "releaser", path: "tools/releaser"

gem "sprockets-rails", ">= 2.0.0", require: false
gem "propshaft", ">= 0.1.7", "!= 1.0.1"
gem "capybara", ">= 3.39"
gem "selenium-webdriver", ">= 4.20.0"

gem "rack-cache", "~> 1.2"
gem "stimulus-rails"
gem "turbo-rails"
gem "jsbundling-rails"
gem "cssbundling-rails"
`
)

func TestRealReactNativePlistRefusesBuildSettings(t *testing.T) {
	// Every version in a stock React Native plist is a build-setting
	// reference. Writing a literal over one freezes the app at that number and
	// stops Xcode driving it.
	path := seed(t, "Info.plist", rnInfoPlist)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten || read(t, path) != rnInfoPlist {
		t.Errorf("a stock React Native plist must come back untouched: %+v", res)
	}
}

func TestRealFlutterPubspecRoundTrip(t *testing.T) {
	path := seed(t, "pubspec.yaml", flutterPubspec)
	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "pigeon", Kind: "devDependencies", Range: ">=27.0.0 <28.0.0"},
		// Declared as a block with a path, so there is no constraint to write.
		{Name: "analysis_defaults", Kind: "devDependencies", Range: "^1.0.0"},
		{Name: "flutter", Range: "^3.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The + suffix is pub's build counter and a version write never touches a
	// build counter, so it rides along.
	want := strings.NewReplacer(
		"version: 1.0.0+1", "version: 2.0.0+1",
		`pigeon: ">=11.0.0 <27.0.0"`, `pigeon: ">=27.0.0 <28.0.0"`,
	).Replace(flutterPubspec)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	// The local path and the sdk block survive; both are skipped, not missing.
	if !strings.Contains(read(t, path), "path: ../../../analysis_defaults") {
		t.Error("a block dependency's path was rewritten")
	}
	if !strings.Contains(read(t, path), "sdk: flutter") {
		t.Error("an sdk dependency was rewritten")
	}
	if len(res.Applied) != 1 || len(res.Skipped) != 2 || len(res.Missing) != 0 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestRealTokioCargoRoundTrip(t *testing.T) {
	path := seed(t, "Cargo.toml", tokioCargoToml)
	res, err := Rewrite(path, "1.54.0", []Edit{
		{Name: "pin-project-lite", Range: "0.2.14"},
		{Name: "tokio-macros", Range: "~2.8.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`version = "1.53.1"`, `version = "1.54.0"`,
		`version = "~2.7.0"`, `version = "~2.8.0"`,
		`pin-project-lite = "0.2.11"`, `pin-project-lite = "0.2.14"`,
	).Replace(tokioCargoToml)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	// The release checklist comment between the name and the version is what
	// makes this file worth testing; it must survive intact.
	if !strings.Contains(read(t, path), "# - Create \"v1.x.y\" git tag.") {
		t.Error("the comment block between name and version was disturbed")
	}
	if !res.VersionWritten || len(res.Applied) != 2 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestRealRailsGemfileRoundTrip(t *testing.T) {
	path := seed(t, "Gemfile", railsGemfile)
	res, err := Rewrite(path, "", []Edit{
		{Name: "minitest", Range: "~> 6.1"},
		// Two requirements are one constraint spread over two literals.
		{Name: "propshaft", Range: ">= 1.0.0"},
		// A path-pinned gem has no version to write.
		{Name: "releaser", Range: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(railsGemfile, `gem "minitest", "~> 6.0"`, `gem "minitest", "~> 6.1"`, 1)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if len(res.Applied) != 1 || len(res.Skipped) != 2 || len(res.Missing) != 0 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestRealFlutterPubspecLinkRoundTrip(t *testing.T) {
	// The pubspec that already carries a block dependency, which is the shape
	// an override lookup has to tell apart from a package actually named path.
	path := seed(t, "pubspec.yaml", flutterPubspec)
	res, err := Relink(path, []Link{{Name: "pigeon", Path: "../forks/pigeon"}})
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, "dependency_overrides:") || !strings.Contains(got, "    path: ../forks/pigeon") {
		t.Errorf("the override was not written:\n%s", got)
	}
	// The existing analysis_defaults path dependency is untouched.
	if !strings.Contains(got, "path: ../../../analysis_defaults") {
		t.Error("an existing block dependency's folder was disturbed")
	}
	if len(res.Applied) != 1 {
		t.Errorf("result mismatch: %+v", res)
	}
	// Removing it returns the file to exactly what it was.
	if _, err := Relink(path, []Link{{Name: "pigeon"}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != flutterPubspec {
		t.Errorf("round trip did not return the original:\n got: %q\nwant: %q", got, flutterPubspec)
	}
}

func TestRealTokioCargoLinkRoundTrip(t *testing.T) {
	path := seed(t, "Cargo.toml", tokioCargoToml)
	if _, err := Relink(path, []Link{{Name: "pin-project-lite", Path: "../forks/pin-project-lite"}}); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, "[patch.crates-io]") {
		t.Errorf("the patch table was not created:\n%s", got)
	}
	// The release checklist comment between the name and the version survives.
	if !strings.Contains(got, "# - Create \"v1.x.y\" git tag.") {
		t.Error("the comment block was disturbed")
	}
	if _, err := Relink(path, []Link{{Name: "pin-project-lite"}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != tokioCargoToml {
		t.Errorf("round trip did not return the original:\n got: %q\nwant: %q", got, tokioCargoToml)
	}
}
