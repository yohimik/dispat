package scanner

// Fixtures fetched from public repositories, trimmed to the parts that matter
// and otherwise byte-for-byte. They exist because hand-written fixtures agree
// with the parser by construction, and these do not: every one of them broke an
// assumption at least once.
//
//	React Native 0.76  github.com/react-native-community/template, 0.76-stable
//	React Native 0.71  github.com/facebook/react-native, v0.71.0
//	Flutter add_to_app github.com/flutter/samples, main

import (
	"reflect"
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

	rnAndroidManifest = `<manifest xmlns:android="http://schemas.android.com/apk/res/android">

    <uses-permission android:name="android.permission.INTERNET" />

    <application
      android:name=".MainApplication"
      android:label="@string/app_name"
      android:icon="@mipmap/ic_launcher"
      android:roundIcon="@mipmap/ic_launcher_round"
      android:allowBackup="false"
      android:theme="@style/AppTheme"
      android:supportsRtl="true">
      <activity
        android:name=".MainActivity"
        android:label="@string/app_name"
        android:configChanges="keyboard|keyboardHidden|orientation|screenLayout|screenSize|smallestScreenSize|uiMode"
        android:launchMode="singleTask"
        android:windowSoftInputMode="adjustResize"
        android:exported="true">
        <intent-filter>
            <action android:name="android.intent.action.MAIN" />
            <category android:name="android.intent.category.LAUNCHER" />
        </intent-filter>
      </activity>
    </application>
</manifest>
`

	rnLegacyBuildGradle = `android {
    ndkVersion rootProject.ext.ndkVersion

    compileSdkVersion rootProject.ext.compileSdkVersion

    namespace "com.helloworld"
    defaultConfig {
        applicationId "com.helloworld"
        minSdkVersion rootProject.ext.minSdkVersion
        targetSdkVersion rootProject.ext.targetSdkVersion
        versionCode 1
        versionName "1.0"
    }

    splits {
        abi {
            reset()
            enable enableSeparateBuildPerCPUArchitecture
            universalApk false  // If true, also generate a universal APK
            include (*reactNativeArchitectures())
        }
    }
    signingConfigs {
        debug {
            storeFile file('debug.keystore')
            storePassword 'android'
            keyAlias 'androiddebugkey'
            keyPassword 'android'
        }
    }
    buildTypes {
        debug {
            signingConfig signingConfigs.debug
        }
        release {
            // Caution! In production, you need to generate your own keystore file.
            // see https://reactnative.dev/docs/signed-apk-android.
            signingConfig signingConfigs.debug
            minifyEnabled enableProguardInReleaseBuilds
            proguardFiles getDefaultProguardFile("proguard-android.txt"), "proguard-rules.pro"
        }
    }

    // applicationVariants are e.g. debug, release
    applicationVariants.all { variant ->
        variant.outputs.each { output ->
            // For each separate APK per architecture, set a unique version code as described here:
            // https://developer.android.com/studio/build/configure-apk-splits.html
            // Example: versionCode 1 will generate 1001 for armeabi-v7a, 1002 for x86, etc.
            def versionCodes = ["armeabi-v7a": 1, "x86": 2, "arm64-v8a": 3, "x86_64": 4]
            def abi = output.getFilter(OutputFile.ABI)
            if (abi != null) {  // null for the universal-debug, universal-release variants
                output.versionCodeOverride =
                        defaultConfig.versionCode * 1000 + versionCodes.get(abi)
            }

        }
    }
}

dependencies {
    // The version of react-native is set by the React Native Gradle Plugin
    implementation("com.facebook.react:react-android")

    implementation("androidx.swiperefreshlayout:swiperefreshlayout:1.0.0")

    debugImplementation("com.facebook.flipper:flipper:${FLIPPER_VERSION}")
    debugImplementation("com.facebook.flipper:flipper-network-plugin:${FLIPPER_VERSION}") {
        exclude group:'com.squareup.okhttp3', module:'okhttp'
    }

    debugImplementation("com.facebook.flipper:flipper-fresco-plugin:${FLIPPER_VERSION}")
    if (hermesEnabled.toBoolean()) {
        implementation("com.facebook.react:hermes-android")
    } else {
        implementation jscFlavor
    }
}
`

	flutterBuildGradle = `apply plugin: 'com.android.application'
apply plugin: 'kotlin-android'
apply plugin: 'kotlin-android-extensions'

android {
    compileSdkVersion 31
    buildToolsVersion "29.0.3"

    defaultConfig {
        applicationId "dev.flutter.example.books"
        minSdkVersion 21
        targetSdkVersion 29
        versionCode 1
        versionName "1.0"

        testInstrumentationRunner "androidx.test.runner.AndroidJUnitRunner"
    }

    buildTypes {
        profile {
            initWith debug
        }
        release {
            minifyEnabled false
            proguardFiles getDefaultProguardFile('proguard-android-optimize.txt'), 'proguard-rules.pro'
            signingConfig debug.signingConfig
        }
    }
    compileOptions {
        sourceCompatibility JavaVersion.VERSION_1_8
        targetCompatibility JavaVersion.VERSION_1_8
    }
    kotlinOptions {
        jvmTarget = "1.8"
    }
}

dependencies {
    implementation fileTree(dir: "libs", include: ["*.jar"])
    implementation "com.squareup.okhttp3:okhttp:4.7.2"
    implementation "org.jetbrains.kotlin:kotlin-stdlib:$kotlin_version"
    implementation 'androidx.core:core-ktx:1.3.0'
    implementation 'androidx.appcompat:appcompat:1.1.0'
    implementation "androidx.activity:activity-ktx:1.1.0"
    implementation 'androidx.constraintlayout:constraintlayout:1.1.3'
    implementation 'com.google.android.material:material:1.1.0'
    implementation 'com.google.code.gson:gson:2.8.6'
    implementation project(path: ':flutter')
    testImplementation 'junit:junit:4.13'
    androidTestImplementation 'androidx.test.ext:junit:1.1.1'
    androidTestImplementation 'androidx.test.espresso:espresso-core:3.2.0'

}`

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

	flutterPodfile = `# Uncomment the next line to define a global platform for your project
# platform :ios, '9.0'

flutter_application_path = '../flutter_module_books/'
load File.join(flutter_application_path, '.ios', 'Flutter', 'podhelper.rb')

target 'IosBooks' do
  # Comment the next line if you don't want to use dynamic frameworks
  use_frameworks!
  # Pods for IosBooks
  install_all_flutter_pods(flutter_application_path)
end

post_install do |installer|
  flutter_post_install(installer) if defined?(flutter_post_install)
end
`
)

func TestRealReactNativeInfoPlist(t *testing.T) {
	// Every value the plist declares is a build-setting reference. The version
	// is kept as written, but the identifier is dropped: every React Native app
	// spells it identically, and NameIndex would report the shared literal as
	// an ambiguous name across unrelated projects.
	dir := t.TempDir()
	write(t, dir, "Info.plist", rnInfoPlist)
	m := scanOne(t, dir)
	if m.Name != "" {
		t.Errorf("name = %q, want empty for $(PRODUCT_BUNDLE_IDENTIFIER)", m.Name)
	}
	if m.Version != "$(MARKETING_VERSION)" || m.BuildNumber != "$(CURRENT_PROJECT_VERSION)" {
		t.Errorf("references not kept verbatim: %+v", m)
	}
}

func TestRealReactNativeAndroidManifest(t *testing.T) {
	// Current React Native declares no package and no versions here; they live
	// in the build script. Reading nothing is the correct answer.
	dir := t.TempDir()
	write(t, dir, "AndroidManifest.xml", rnAndroidManifest)
	m := scanOne(t, dir)
	want := Manifest{Path: "AndroidManifest.xml", Ecosystem: EcosystemAndroid, Root: true}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestRealReactNativeLegacyBuildGradle(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "build.gradle", rnLegacyBuildGradle)
	m := scanOne(t, dir)
	if m.Name != "com.helloworld" || m.Version != "1.0" || m.BuildNumber != "1" {
		t.Errorf("identity mismatch: %+v", m)
	}
	// The Flipper dependencies interpolate their version and jscFlavor is a
	// variable, so neither can be read from this file alone. The two-part
	// coordinates carry no version but still name a package.
	want := []DeclaredDep{
		{Name: "androidx.swiperefreshlayout:swiperefreshlayout", Range: "1.0.0", Kind: KindDependencies},
		{Name: "com.facebook.react:hermes-android", Kind: KindDependencies},
		{Name: "com.facebook.react:react-android", Kind: KindDependencies},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps mismatch:\n got %+v\nwant %+v", m.Deps, want)
	}
}

func TestRealFlutterBuildGradle(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "build.gradle", flutterBuildGradle)
	m := scanOne(t, dir)
	if m.Name != "dev.flutter.example.books" || m.Version != "1.0" || m.BuildNumber != "1" {
		t.Errorf("identity mismatch: %+v", m)
	}
	want := []DeclaredDep{
		{Name: "androidx.activity:activity-ktx", Range: "1.1.0", Kind: KindDependencies},
		{Name: "androidx.appcompat:appcompat", Range: "1.1.0", Kind: KindDependencies},
		{Name: "androidx.constraintlayout:constraintlayout", Range: "1.1.3", Kind: KindDependencies},
		{Name: "androidx.core:core-ktx", Range: "1.3.0", Kind: KindDependencies},
		{Name: "com.google.android.material:material", Range: "1.1.0", Kind: KindDependencies},
		{Name: "com.google.code.gson:gson", Range: "2.8.6", Kind: KindDependencies},
		{Name: "com.squareup.okhttp3:okhttp", Range: "4.7.2", Kind: KindDependencies},
		// project(path: ':flutter'), named-argument form, no local path because
		// a Gradle project path is relative to the build root.
		{Name: "flutter", Kind: KindDependencies},
		{Name: "androidx.test.espresso:espresso-core", Range: "3.2.0", Kind: KindDevDependencies},
		{Name: "androidx.test.ext:junit", Range: "1.1.1", Kind: KindDevDependencies},
		{Name: "junit:junit", Range: "4.13", Kind: KindDevDependencies},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps mismatch:\n got %+v\nwant %+v", m.Deps, want)
	}
}

func TestRealFlutterPubspec(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pubspec.yaml", flutterPubspec)
	m := scanOne(t, dir)
	// Dart versions carry a +build suffix, kept as written.
	if m.Name != "flutter_module_books" || m.Version != "1.0.0+1" {
		t.Errorf("identity mismatch: %+v", m)
	}
	want := []DeclaredDep{
		{Name: "flutter", Kind: KindDependencies},
		{Name: "analysis_defaults", Kind: KindDevDependencies, LocalPath: "../../../analysis_defaults"},
		{Name: "flutter_test", Kind: KindDevDependencies},
		{Name: "pigeon", Range: ">=11.0.0 <27.0.0", Kind: KindDevDependencies},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps mismatch:\n got %+v\nwant %+v", m.Deps, want)
	}
}

func TestRealFlutterPodfile(t *testing.T) {
	// A Flutter Podfile installs its pods through a helper, so there is no pod
	// statement to read. An empty result is correct, not a parse failure.
	dir := t.TempDir()
	write(t, dir, "Podfile", flutterPodfile)
	m := scanOne(t, dir)
	if len(m.Deps) != 0 {
		t.Errorf("a helper-driven Podfile declares nothing readable: %+v", m.Deps)
	}
}

const (
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

	railsGemspec = `Gem::Specification.new do |s|
  s.platform    = Gem::Platform::RUBY
  s.name        = "activesupport"
  s.version     = version
  s.summary     = "A toolkit of support libraries and Ruby core extensions extracted from the Rails framework."
  s.description = "A toolkit of support libraries and Ruby core extensions extracted from the Rails framework. Rich support for multibyte strings, internationalization, time zones, and testing."

  s.required_ruby_version = ">= 3.3.1"

  s.license = "MIT"

  s.author   = "David Heinemeier Hansson"
  s.email    = "david@loudthinking.com"
  s.homepage = "https://rubyonrails.org"

  s.files        = Dir["CHANGELOG.md", "MIT-LICENSE", "README.rdoc", "lib/**/*"]
  s.require_path = "lib"

  s.rdoc_options.concat ["--encoding",  "UTF-8"]

  s.metadata = {
    "bug_tracker_uri"   => "https://github.com/rails/rails/issues",
    "changelog_uri"     => "https://github.com/rails/rails/blob/v#{version}/activesupport/CHANGELOG.md",
    "documentation_uri" => "https://api.rubyonrails.org/v#{version}/",
    "mailing_list_uri"  => "https://discuss.rubyonrails.org/c/rubyonrails-talk",
    "source_code_uri"   => "https://github.com/rails/rails/tree/v#{version}/activesupport",
    "rubygems_mfa_required" => "true",
  }

  # NOTE: Please read our dependency guidelines before updating versions:
  # https://edgeguides.rubyonrails.org/security.html#dependency-management-and-cves

  s.add_dependency "i18n",            ">= 1.6", "< 2"
  s.add_dependency "tzinfo",          "~> 2.0", ">= 2.0.5"
  s.add_dependency "concurrent-ruby", "~> 1.0", ">= 1.3.1"
  s.add_dependency "connection_pool", ">= 2.2.5"
  s.add_dependency "minitest",        ">= 5.1"
  s.add_dependency "base64"
  s.add_dependency "drb"
  s.add_dependency "bigdecimal"
  s.add_dependency "json"
  s.add_dependency "psych", ">= 4"
  s.add_dependency "logger", ">= 1.4.2"
  s.add_dependency "securerandom", ">= 0.3"
  s.add_dependency "uri", ">= 0.13.1"
  s.add_dependency "ractor-dispatch", ">= 0.2.0"
end
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

	nowInAndroidCatalog = `[versions]
accompanist = "0.37.0"
androidDesugarJdkLibs = "2.1.4"
# AGP and tools should be updated together
androidGradlePlugin = "9.0.0"
androidTools = "32.0.0"
androidxActivity = "1.9.3"
androidxAppCompat = "1.7.0"

[libraries]
accompanist-permissions = { group = "com.google.accompanist", name = "accompanist-permissions", version.ref = "accompanist" }
android-desugarJdkLibs = { group = "com.android.tools", name = "desugar_jdk_libs", version.ref = "androidDesugarJdkLibs" }
androidx-activity-compose = { group = "androidx.activity", name = "activity-compose", version.ref = "androidxActivity" }
androidx-appcompat = { group = "androidx.appcompat", name = "appcompat", version.ref = "androidxAppCompat" }
androidx-benchmark-macro = { group = "androidx.benchmark", name = "benchmark-macro-junit4", version.ref = "androidxMacroBenchmark" }
androidx-browser = { group = "androidx.browser", name = "browser", version.ref = "androidxBrowser" }
androidx-compose-bom = { group = "androidx.compose", name = "compose-bom-alpha", version.ref = "androidxComposeBom" }
`
)

func TestRealRailsGemfile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Gemfile", railsGemfile)
	m := scanOne(t, dir)
	byName := map[string]DeclaredDep{}
	for _, d := range m.Deps {
		byName[d.Name] = d
	}
	for _, tc := range []struct{ name, rng, local string }{
		{"minitest", "~> 6.0", ""},
		{"minitest-mock", "", ""},
		// A path option in the newer hash spelling.
		{"releaser", "", "tools/releaser"},
		// A requirement followed by an option: the option is not a requirement.
		{"sprockets-rails", ">= 2.0.0", ""},
		// Two requirements are one constraint, reported as one range.
		{"propshaft", ">= 0.1.7, != 1.0.1", ""},
	} {
		got, ok := byName[tc.name]
		if !ok {
			t.Errorf("%s not read", tc.name)
			continue
		}
		if got.Range != tc.rng || got.LocalPath != tc.local {
			t.Errorf("%s = %+v, want range %q local %q", tc.name, got, tc.rng, tc.local)
		}
	}
}

func TestRealRailsGemspec(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "activesupport.gemspec", railsGemspec)
	m := scanOne(t, dir)
	// The version is read from a file at load time, so there is no literal.
	if m.Name != "activesupport" || m.Version != "" {
		t.Errorf("identity mismatch: %+v", m)
	}
	byName := map[string]string{}
	for _, d := range m.Deps {
		byName[d.Name] = d.Range
	}
	for name, want := range map[string]string{
		"i18n":            ">= 1.6, < 2",
		"tzinfo":          "~> 2.0, >= 2.0.5",
		"concurrent-ruby": "~> 1.0, >= 1.3.1",
		"connection_pool": ">= 2.2.5",
		"base64":          "",
	} {
		if got, ok := byName[name]; !ok || got != want {
			t.Errorf("%s = %q (present %v), want %q", name, got, ok, want)
		}
	}
}

func TestRealTokioCargoToml(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Cargo.toml", tokioCargoToml)
	m := scanOne(t, dir)
	// Six comment lines sit between the name and the version.
	if m.Name != "tokio" || m.Version != "1.53.1" {
		t.Errorf("identity mismatch: %+v", m)
	}
	byName := map[string]string{}
	for _, d := range m.Deps {
		byName[d.Name] = d.Range
	}
	if byName["tokio-macros"] != "~2.7.0" || byName["pin-project-lite"] != "0.2.11" {
		t.Errorf("deps mismatch: %+v", m.Deps)
	}
}

func TestRealNowInAndroidCatalog(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "gradle/libs.versions.toml", nowInAndroidCatalog)
	m := scanOne(t, dir)
	byName := map[string]string{}
	for _, d := range m.Deps {
		byName[d.Name] = d.Range
	}
	// Separate group and name keys, each resolved through version.ref.
	for name, want := range map[string]string{
		"com.google.accompanist:accompanist-permissions": "0.37.0",
		"com.android.tools:desugar_jdk_libs":             "2.1.4",
		"androidx.activity:activity-compose":             "1.9.3",
		"androidx.appcompat:appcompat":                   "1.7.0",
	} {
		if got, ok := byName[name]; !ok || got != want {
			t.Errorf("%s = %q (present %v), want %q", name, got, ok, want)
		}
	}
	// A ref this excerpt does not define resolves to nothing rather than to a
	// guess, and the coordinate still carries the graph.
	if got, ok := byName["androidx.browser:browser"]; !ok || got != "" {
		t.Errorf("unresolved ref = %q (present %v), want empty", got, ok)
	}
}
