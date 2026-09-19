package publicapi_test

// Format-by-format reading through the public scanner. Each fixture is a
// realistic file rather than a minimal one, because the shapes that decide
// what a manifest declares (a Groovy comment, a Ruby interpolation, a Poetry
// table, a nested plist dictionary) only appear in files that look like the
// ones a repository actually holds.

import (
	"errors"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
	"github.com/yohimik/dispat/pkg/scanner"
)

func TestPublicAPIScannerReadsGradleBuildScripts(t *testing.T) {
	t.Run("an application module", func(t *testing.T) {
		m := scanFixture(t, "build.gradle", `/*
 * Acme Android application.
 * The build script is Groovy.
 */
buildscript {
    dependencies {
        classpath 'com.android.tools.build:gradle:8.5.0'
    }
}

android {
    namespace 'com.acme.namespace'
    defaultConfig {
        applicationId "com.acme.app" // the identifier the store sees
        versionName '1.0.0'
        versionNameSuffix '-dev'
        versionCode 7
        manifestPlaceholders = [label: "Acme\"s App"]
    }
    buildTypes {
        release {
            versionName '9.9.9'
        }
    }
}

dependencies {
    implementation 'androidx.core:core-ktx:1.12.0'
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    api project(':core')
    implementation(project(path: ":tools")) {
        exclude group: 'org.json'
    }
    testImplementation 'junit:junit:4.13.2'
    androidTestImplementation 'androidx.test.ext:junit:1.1.5'
    debugImplementation 'com.squareup.leakcanary:leakcanary-android:2.14'
    annotationProcessor 'com.google.dagger:dagger-compiler:2.51'
    kapt 'androidx.room:room-compiler:2.6.1'
    ksp 'com.github.bumptech.glide:ksp:4.16.0'
    compileOnly 'javax.annotation:jsr250-api:1.0'
    runtimeOnly 'mysql:mysql-connector-java:8.0.33'
    implementation libs.retrofit
    implementation "com.acme:dynamic:$acmeVersion"
    implementation 'unterminated
    api (project(':shared')) {
        exclude group: 'org.json'
    }
    implementation project(":${moduleName}")
    api project
    implementation project(rootProject.extra)
    implementation project(path "core")
    implementation project(':')
}

configurations.all {
    resolutionStrategy
        {
            force 'com.acme:core:1.0.0'
        }
}
`)
		assertIdentity(t, m, "com.acme.app|1.0.0|7")
		assertDeps(t, m,
			"dependencies|androidx.core:core-ktx|1.12.0|",
			"dependencies|com.squareup.okhttp3:okhttp|4.12.0|",
			"dependencies|core||",
			"dependencies|tools||",
			"dependencies|shared||",
			"dependencies|mysql:mysql-connector-java|8.0.33|",
			"devDependencies|junit:junit|4.13.2|",
			"devDependencies|androidx.test.ext:junit|1.1.5|",
			"devDependencies|com.squareup.leakcanary:leakcanary-android|2.14|",
			"devDependencies|com.google.dagger:dagger-compiler|2.51|",
			"devDependencies|androidx.room:room-compiler|2.6.1|",
			"devDependencies|com.github.bumptech.glide:ksp|4.16.0|",
			"peerDependencies|javax.annotation:jsr250-api|1.0|",
		)
	})

	t.Run("a library module names itself by its namespace", func(t *testing.T) {
		m := scanFixture(t, "build.gradle.kts", `android {
    namespace = "com.acme.lib"
    defaultConfig {
        applicationId = "com.acme.${flavorName}"
        versionName = project.findProperty("libVersion")
        minSdk = 24
    }
}

dependencies {
    implementation("com.acme:core:1.0.0")
}
`)
		assertIdentity(t, m, "com.acme.lib||")
		assertDeps(t, m, "dependencies|com.acme:core|1.0.0|")
	})
}

func TestPublicAPIScannerReadsGradleVersionCatalog(t *testing.T) {
	m := scanFixture(t, "gradle/libs.versions.toml", `[versions]
kotlin = "1.9.24"
okhttp = { require = "4.12.0" }
strict = { strictly = "1.0.0" }
preferred = { prefer = "2.0.0" }

[libraries]
kotlin-stdlib = { module = "org.jetbrains.kotlin:kotlin-stdlib", version.ref = "kotlin" }
okhttp = { group = "com.squareup.okhttp3", name = "okhttp", version.ref = "okhttp" }
shorthand = "androidx.core:core-ktx:1.12.0"
duplicate = "androidx.core:core-ktx:1.12.0"
strictly = { module = "com.acme:strict", version.ref = "strict" }
preferring = { module = "com.acme:preferred", version.ref = "preferred" }
noversion = { module = "com.acme:plain" }
malformed-module = { module = "nocolon" }
malformed-group = { name = "onlyname" }
malformed-short = "onlyone"
too-many = "a:b:c:d"
weird = 42

[bundles]
network = ["okhttp"]

[plugins]
android = { id = "com.android.application", version = "8.5.0" }
`)
	assertIdentity(t, m, "||")
	assertDeps(t, m,
		"dependencies|org.jetbrains.kotlin:kotlin-stdlib|1.9.24|",
		"dependencies|com.squareup.okhttp3:okhttp|4.12.0|",
		"dependencies|androidx.core:core-ktx|1.12.0|",
		"dependencies|com.acme:strict|1.0.0|",
		"dependencies|com.acme:preferred|2.0.0|",
		"dependencies|com.acme:plain||",
	)
}

func TestPublicAPIScannerReadsRubyManifests(t *testing.T) {
	t.Run("podfile", func(t *testing.T) {
		m := scanFixture(t, "Podfile", `# Acme iOS workspace.
platform :ios, '15.0'
source 'https://cdn.cocoapods.org/'

target 'Acme' do
  use_frameworks!
  pod 'Alamofire', '~> 5.8'
  pod 'AcmeCore', :path => '../core'
  pod 'AcmeUI', path: '../ui'
  pod 'Firebase/Analytics', '>= 10.0', '< 11.0'
  pod 'AcmeNet', :git => 'https://example.com/net.git', :tag => 'v1.0.0'
  pod "Acme#Analytics", '1.0.0'
  pod 'It\'s', '2.0.0'
  pod "Esc\"aped", '3.0.0'
  pod 'Dyn', "~> #{DYN_VERSION}"
  pod 'Unterminated
  pod ''
  podspec
  target 'AcmeTests' do
    pod 'Quick', '~> 7.0' # the BDD matchers
  end
end

target acme_target_name do
  pod 'Nimble'
end
`)
		assertIdentity(t, m, "||")
		assertDeps(t, m,
			"dependencies|Alamofire|~> 5.8|",
			"dependencies|AcmeCore||../core",
			"dependencies|AcmeUI||../ui",
			"dependencies|Firebase/Analytics|>= 10.0, < 11.0|",
			"dependencies|AcmeNet||",
			"dependencies|Acme#Analytics|1.0.0|",
			`dependencies|It\'s|2.0.0|`,
			`dependencies|Esc\"aped|3.0.0|`,
			"dependencies|Dyn||",
			"devDependencies|Quick|~> 7.0|",
			"devDependencies|Nimble||",
		)
	})

	t.Run("gemfile", func(t *testing.T) {
		m := scanFixture(t, "Gemfile", `# Acme application gems.
source 'https://rubygems.org'
ruby '3.3.0'

gem 'rails', '~> 7.1'
gem 'pg', '>= 0.18', '< 2.0'
gem 'acme-core', path: '../core'
gem 'acme-ui', git: 'https://git.example.com/ui.git', glob: 'gems/:path/*.gemspec'
gem 'sidekiq', require: false

group :development, :test do
  gem 'rspec-rails', '~> 6.1'
  gem 'factory_bot'
end

group :production do
  gem 'newrelic_rpm'
end

gem 'pry', group: :development
`)
		assertDeps(t, m,
			"dependencies|rails|~> 7.1|",
			"dependencies|pg|>= 0.18, < 2.0|",
			"dependencies|acme-core||../core",
			"dependencies|acme-ui||",
			"dependencies|sidekiq||",
			"dependencies|newrelic_rpm||",
			"devDependencies|rspec-rails|~> 6.1|",
			"devDependencies|factory_bot||",
			"devDependencies|pry||",
		)
	})

	t.Run("podspec", func(t *testing.T) {
		m := scanFixture(t, "AcmeKit.podspec", `Pod::Spec.new do |s|
  s.name              = 'AcmeKit'
  s.version           = '1.0.0'
  s.summary           = 'Acme # Kit'
  s.description       = "Acme #{s.name} toolkit"
  s.static_framework  = true
  s.source            = { :git => 'https://example.com/acme.git', :tag => "v#{s.version}" }
  s.dependency 'Alamofire', '~> 5.8'
  s.dependency 'AcmeCore', :path => '../core'
  s.ios.dependency 'AcmeUI'
  s.version == '9.9.9'
  s.deployment_target => '15.0'
  dependency 'NotDotted'
  s.name = 'Later'
  s.subspec 'Core' do |ss|
    ss.dependency 'AcmeInternal', '~> 1.0'
  end
end
`)
		assertIdentity(t, m, "AcmeKit|1.0.0|")
		assertDeps(t, m,
			"dependencies|Alamofire|~> 5.8|",
			"dependencies|AcmeCore||../core",
			"dependencies|AcmeUI||",
			"dependencies|AcmeInternal|~> 1.0|",
		)
	})

	t.Run("gemspec", func(t *testing.T) {
		m := scanFixture(t, "acme.gemspec", `# frozen_string_literal: true

require_relative 'lib/acme/version'

Gem::Specification.new do |spec|
  spec.name          = 'acme'
  spec.version       = Acme::VERSION
  spec.summary       = "Acme #{spec.name}"
  spec.metadata['source_code_uri'] = 'https://example.com'
  spec.add_dependency 'rack', '>= 2.0', '< 4.0'
  spec.add_runtime_dependency 'nokogiri', '~> 1.16'
  spec.add_development_dependency 'rspec', '~> 3.13'
  spec.add_dependency 'acme-core', path: '../core'
end
`)
		assertIdentity(t, m, "acme||")
		assertDeps(t, m,
			"dependencies|rack|>= 2.0, < 4.0|",
			"dependencies|nokogiri|~> 1.16|",
			"dependencies|acme-core||../core",
			"devDependencies|rspec|~> 3.13|",
		)
	})
}

func TestPublicAPIScannerReadsPythonManifests(t *testing.T) {
	t.Run("pep 621 project", func(t *testing.T) {
		m := scanFixture(t, "pyproject.toml", `[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[project]
name = "Acme_Core"
version = "1.0.0"
dependencies = [
    "requests[socks]>=2.0,<3; python_version > '3.8'",
    "acme-io @ file:../io",
    "acme-remote @ https://example.com/acme.whl",
    "acme-sibling @ ../sibling",
    "tomli ; python_version < '3.11'",
    "packaging (>=23.0)",
]

[project.optional-dependencies]
cli = ["click>=8.0"]

[dependency-groups]
dev = ["pytest>=8.0", {include-group = "test"}]
`)
		assertIdentity(t, m, "acme-core|1.0.0|")
		assertDeps(t, m,
			"dependencies|requests|>=2.0,<3|",
			"dependencies|acme-io|@ file:../io|../io",
			"dependencies|acme-remote|@ https://example.com/acme.whl|",
			"dependencies|acme-sibling|@ ../sibling|../sibling",
			"dependencies|tomli||",
			"dependencies|packaging|>=23.0|",
			"devDependencies|pytest|>=8.0|",
			"optionalDependencies|click|>=8.0|",
		)
	})

	t.Run("poetry project", func(t *testing.T) {
		m := scanFixture(t, "pyproject.toml", `[tool.poetry]
name = "Acme.App"
version = "2.0.0"

[tool.poetry.dependencies]
python = "^3.11"
requests = "^2.31"
acme-core = { path = "../core", develop = true }
acme-db = { version = "^1.0", optional = true }
acme-plat = [
    { version = "^1.0", markers = "sys_platform == 'linux'" },
]

[tool.poetry.dev-dependencies]
pytest = "^8.0"

[tool.poetry.group.docs.dependencies]
sphinx = "^7.0"
`)
		assertIdentity(t, m, "acme-app|2.0.0|")
		assertDeps(t, m,
			"dependencies|requests|^2.31|",
			"dependencies|acme-core||../core",
			"dependencies|acme-db|^1.0|",
			"devDependencies|pytest|^8.0|",
			"devDependencies|sphinx|^7.0|",
		)
	})

	t.Run("requirements files", func(t *testing.T) {
		dir := writeTree(t, t.TempDir(), map[string]string{
			"requirements.txt": "# Runtime requirements.\n" +
				"--index-url https://pypi.example.com/simple\n" +
				"-r constraints.txt\n" +
				"acme-core==1.0.0 \\\n" +
				"    --hash=sha256:abcdef\n" +
				"acme-tab==2.0.0\t--hash=sha256:beef\n" +
				"requests>=2.0  # the HTTP client\n" +
				"./vendor/local-wheel\n" +
				"https://example.com/acme.whl\n" +
				"-e ./packages/acme-io\n" +
				"--editable ../shared\n" +
				"-e git+https://example.com/x.git#egg=x\n" +
				"flask ==3.0.0\n",
			"requirements-dev.txt":    "pytest>=8.0\n",
			"requirements-latest.txt": "urllib3>=2.0\n",
		})
		mans, err := scanner.Scan(t.Context(), dir)
		if err != nil || len(mans) != 3 {
			t.Fatalf("scan = %+v, err = %v", mans, err)
		}
		byPath := map[string]scanner.Manifest{}
		for _, m := range mans {
			byPath[m.Path] = m
		}
		assertDeps(t, byPath["requirements.txt"],
			"dependencies|acme-core|==1.0.0|",
			"dependencies|acme-tab|==2.0.0|",
			"dependencies|requests|>=2.0|",
			"dependencies|flask|==3.0.0|",
			"dependencies|acme-io||./packages/acme-io",
			"dependencies|shared||../shared",
		)
		assertDeps(t, byPath["requirements-dev.txt"], "devDependencies|pytest|>=8.0|")
		assertDeps(t, byPath["requirements-latest.txt"], "dependencies|urllib3|>=2.0|")
	})
}

func TestPublicAPIScannerReadsPubspecAndCargo(t *testing.T) {
	t.Run("pubspec overrides annotate their declaration", func(t *testing.T) {
		m := scanFixture(t, "pubspec.yaml", `name: acme_app
version: 1.2.3+45

environment:
  sdk: '>=3.0.0 <4.0.0'

dependencies:
  flutter:
    sdk: flutter
  acme_core: ^1.0.0
  acme_ui:
  acme_net:
    version: ^2.0.0
    hosted: https://pub.example.com
  acme_bad: 42

dev_dependencies:
  flutter_test:
    sdk: flutter
  mocktail: ^1.0.0

dependency_overrides:
  acme_core:
    path: ../core
  acme_ui:
    path: ../ui
    version: ^3.0.0
  acme_extra:
    path: ../extra
`)
		assertIdentity(t, m, "acme_app|1.2.3+45|45")
		assertDeps(t, m,
			"dependencies|flutter||",
			"dependencies|acme_core|^1.0.0|../core",
			"dependencies|acme_ui|^3.0.0|../ui",
			"dependencies|acme_net|^2.0.0|",
			"dependencies|acme_extra||../extra",
			"devDependencies|flutter_test||",
			"devDependencies|mocktail|^1.0.0|",
		)
		assertDropped(t, m, "dependency acme_bad: unreadable value shape")
	})

	t.Run("a pubspec declaring no identity reads as empty", func(t *testing.T) {
		m := scanFixture(t, "pubspec.yaml", "dependencies:\n  acme_core: ^1.0.0\n")
		assertIdentity(t, m, "||")
		assertDeps(t, m, "dependencies|acme_core|^1.0.0|")
	})

	t.Run("a pubspec version typed as a float still reads as text", func(t *testing.T) {
		m := scanFixture(t, "pubspec.yaml", "name: acme_lib\nversion: 1.0\n")
		assertIdentity(t, m, "acme_lib|1|")
	})

	t.Run("cargo inline tables, renames and workspace inheritance", func(t *testing.T) {
		m := scanFixture(t, "Cargo.toml", `[package]
name = "acme-app"
version = "1.0.0"
edition = "2021"

[dependencies]
serde = "1.0"
serde_json = { version = "1.0", features = ["preserve_order"] }
acme-core = { path = "../core", version = "1.0" }
renamed = { package = "real-crate", version = "2.0" }
twice = { version = "1.0", path = "../twice" }
broken = 42

[dev-dependencies]
criterion = "0.5"

[build-dependencies]
cc = "1.0"
acme-core = "0.9"
twice = { version = "1.0", path = "../twice-build" }
`)
		assertIdentity(t, m, "acme-app|1.0.0|")
		assertDeps(t, m,
			"dependencies|serde|1.0|",
			"dependencies|serde_json|1.0|",
			"dependencies|acme-core|1.0|../core",
			"dependencies|acme-core|0.9|",
			"dependencies|twice|1.0|../twice-build",
			"dependencies|twice|1.0|../twice",
			"dependencies|real-crate|2.0|",
			"dependencies|cc|1.0|",
			"devDependencies|criterion|0.5|",
		)
		assertDropped(t, m, "dependency broken: unreadable value shape")
	})

	t.Run("a workspace-inherited cargo version is not a literal", func(t *testing.T) {
		m := scanFixture(t, "Cargo.toml", `[package]
name = "acme-member"
version = { workspace = true }

[dependencies]
acme-core = { workspace = true }
`)
		assertIdentity(t, m, "acme-member||")
		assertDeps(t, m, "dependencies|acme-core||")
	})
}

func TestPublicAPIScannerReadsAquaConfigurations(t *testing.T) {
	m := scanFixture(t, "aqua.yaml", `---
# aqua - Declarative CLI Version Manager
registries:
  - type: standard
    ref: v4.200.0

packages:
  - name: cli/cli@v2.55.0
  - name: junegunn/fzf
    version: v0.54.0
  - name: acme/tool
    version: v1.0.0
    registry: acme
  - name: golang/go
    go_version_file: .go-version
  - name: dynamic/tool
    version_expr: semver(">= 1.0.0")
  - name: unpinned/tool
  - name: acme/twice@v1.0.0
  - name: acme/twice@v2.0.0
`)
	assertDeps(t, m,
		"dependencies|cli/cli|v2.55.0|",
		"dependencies|junegunn/fzf|v0.54.0|",
		"dependencies|acme:acme/tool|v1.0.0|",
		"dependencies|acme/twice|v1.0.0|",
		"dependencies|acme/twice|v2.0.0|",
	)
	assertDropped(t, m,
		"golang/go: go_version_file is dynamic and was not read",
		"dynamic/tool: version_expr is dynamic and was not evaluated",
		"unpinned/tool: no literal version",
	)

	t.Run("a configuration listing no packages declares nothing", func(t *testing.T) {
		empty := scanFixture(t, "aqua.yaml", `registries:
  - type: standard
    ref: v4.200.0
`)
		assertDeps(t, empty)
	})

	t.Run("an anchored package list is followed through its alias", func(t *testing.T) {
		aliased := scanFixture(t, "aqua.yaml", `base: &base
  - name: cli/cli@v2.55.0
packages: *base
`)
		assertDeps(t, aliased, "dependencies|cli/cli|v2.55.0|")
	})

	t.Run("an import list the typed reader cannot decode is reported", func(t *testing.T) {
		dir := writeTree(t, t.TempDir(), map[string]string{
			"aqua.yaml": "import_dir:\n  - imports\npackages:\n  - name: cli/cli@v2.55.0\n",
		})
		if _, err := scanner.Scan(t.Context(), dir); err == nil {
			t.Fatal("a malformed import_dir was not reported")
		}
	})
}

func TestPublicAPIScannerReadsAppleManifests(t *testing.T) {
	t.Run("info.plist reads only the root dictionary", func(t *testing.T) {
		m := scanFixture(t, "Info.plist", `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>com.acme.app</string>
	<key>CFBundleShortVersionString</key>
	<string>1.0.0</string>
	<key>CFBundleVersion</key>
	<string>42</string>
	<key>LSRequiresIPhoneOS</key>
	<true/>
	<key>CFBundleURLTypes</key>
	<array>
		<dict>
			<key>CFBundleVersion</key>
			<string>nested-must-not-leak</string>
		</dict>
	</array>
	<string>a value with no key before it</string>
	<key>DanglingKey</key>
</dict>
</plist>
`)
		assertIdentity(t, m, "com.acme.app|1.0.0|42")
	})

	t.Run("a build-setting reference is not an identifier", func(t *testing.T) {
		m := scanFixture(t, "Info.plist", `<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>$(PRODUCT_BUNDLE_IDENTIFIER)</string>
	<key>CFBundleShortVersionString</key>
	<string>$(MARKETING_VERSION)</string>
</dict>
</plist>
`)
		assertIdentity(t, m, "|$(MARKETING_VERSION)|")
	})

	t.Run("a plist whose root is not a dictionary declares nothing", func(t *testing.T) {
		m := scanFixture(t, "Info.plist", `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<array>
	<string>com.acme.app</string>
</array>
</plist>
`)
		assertIdentity(t, m, "||")
	})

	t.Run("xcode project settings", func(t *testing.T) {
		m := scanFixture(t, "Acme.xcodeproj/project.pbxproj", `// !$*UTF8*$!
{
	archiveVersion = 1;
	objects = {
		1A2B3C4D /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				CURRENT_PROJECT_VERSION = 42;
				MARKETING_VERSION[sdk=iphoneos*] = 9.9.9;
				MARKETING_VERSION = 1.0.0 ;
				PRODUCT_BUNDLE_IDENTIFIER = "com.acme.app" ;
				EMPTY_SETTING =
				PRODUCT_NAME = "$(TARGET_NAME)";
				INFOPLIST_KEY_CFBundleDisplayName = "Acme \"Pro\"";
				UNTERMINATED = "no closing quote;
				NO_SEMICOLON = 1
			};
			name = Debug;
		};
	};
}
`)
		assertIdentity(t, m, "com.acme.app|1.0.0|42")
	})
}

func TestPublicAPIScannerReadsDotNetAndMavenManifests(t *testing.T) {
	t.Run("csproj names itself and records project references", func(t *testing.T) {
		m := scanFixture(t, "src/Acme.Api/Acme.Api.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup Condition="'$(Configuration)'=='Debug'">
    <AssemblyName>Acme.Api.Debug</AssemblyName>
  </PropertyGroup>
  <PropertyGroup>
    <PackageId>Acme.Api</PackageId>
    <Version>1.0.0</Version>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Serilog" Version="3.1.1" />
    <PackageReference Include="Newtonsoft.Json">
      <Version> 13.0.3 </Version>
    </PackageReference>
    <PackageReference Include="Acme.Central" />
    <ProjectReference Include="..\Acme.Core\Acme.Core.csproj" />
  </ItemGroup>
</Project>
`)
		assertIdentity(t, m, "Acme.Api|1.0.0|")
		assertDeps(t, m,
			"dependencies|Serilog|3.1.1|",
			"dependencies|Newtonsoft.Json|13.0.3|",
			"dependencies|Acme.Central||",
			`dependencies|Acme.Core||../Acme.Core/Acme.Core.csproj`,
		)
	})

	t.Run("csproj falls back to the assembly name then the file name", func(t *testing.T) {
		assembly := scanFixture(t, "Acme.Tool.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <AssemblyName>Acme.Tool.Assembly</AssemblyName>
  </PropertyGroup>
</Project>
`)
		assertIdentity(t, assembly, "Acme.Tool.Assembly||")

		named := scanFixture(t, "Acme.Bare.fsproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>
`)
		assertIdentity(t, named, "Acme.Bare||")
	})

	t.Run("nuspec reads grouped dependencies and drops a token identity", func(t *testing.T) {
		m := scanFixture(t, "Acme.nuspec", `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd">
  <metadata>
    <id>$id$</id>
    <version>$version$</version>
    <dependencies>
      <dependency id="Serilog" version="[3.1.1,4.0.0)" />
      <dependency id="" version="1.0.0" />
      <group targetFramework="net8.0">
        <dependency id="Acme.Core" version="1.0.0" />
      </group>
      <group targetFramework="net6.0">
        <dependency id="Acme.Core" version="1.0.0" />
      </group>
    </dependencies>
  </metadata>
</package>
`)
		assertIdentity(t, m, "|$version$|")
		assertDeps(t, m,
			"dependencies|Serilog|[3.1.1,4.0.0)|",
			"dependencies|Acme.Core|1.0.0|",
		)
	})

	t.Run("central package management and the legacy package list", func(t *testing.T) {
		props := scanFixture(t, "Directory.Packages.props", `<Project>
  <PropertyGroup>
    <ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally>
  </PropertyGroup>
  <ItemGroup>
    <PackageVersion Include="Serilog" Version="3.1.1" />
    <PackageVersion Include="" Version="0.0.0" />
  </ItemGroup>
  <ItemGroup>
    <PackageVersion Include="Acme.Core" Version="1.0.0" />
  </ItemGroup>
</Project>
`)
		assertDeps(t, props,
			"dependencies|Serilog|3.1.1|",
			"dependencies|Acme.Core|1.0.0|",
		)

		config := scanFixture(t, "packages.config", `<?xml version="1.0" encoding="utf-8"?>
<packages>
  <package id="Serilog" version="3.1.1" targetFramework="net48" />
  <package id="StyleCop.Analyzers" version="1.1.118" developmentDependency="TRUE" />
  <package id="" version="1.0.0" />
</packages>
`)
		assertDeps(t, config,
			"dependencies|Serilog|3.1.1|",
			"devDependencies|StyleCop.Analyzers|1.1.118|",
		)
	})

	t.Run("maven inherits its coordinates from the parent", func(t *testing.T) {
		m := scanFixture(t, "pom.xml", `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.acme</groupId>
    <artifactId>acme-parent</artifactId>
    <version>1.0.0</version>
  </parent>
  <artifactId>acme-api</artifactId>
  <dependencies>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>acme-core</artifactId>
      <version>${acme.version}</version>
    </dependency>
    <dependency>
      <groupId>org.junit.jupiter</groupId>
      <artifactId>junit-jupiter</artifactId>
      <version>5.10.2</version>
      <scope>TEST</scope>
    </dependency>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>acme-optional</artifactId>
      <version>1.0.0</version>
      <optional>TRUE</optional>
    </dependency>
    <dependency>
      <artifactId>groupless</artifactId>
      <version>1.0.0</version>
    </dependency>
    <dependency>
      <groupId>com.acme</groupId>
      <version>1.0.0</version>
    </dependency>
  </dependencies>
</project>
`)
		assertIdentity(t, m, "com.acme:acme-api|1.0.0|")
		assertDeps(t, m,
			"dependencies|com.acme:acme-core|${acme.version}|",
			"dependencies|groupless|1.0.0|",
			"dependencies||1.0.0|",
			"devDependencies|org.junit.jupiter:junit-jupiter|5.10.2|",
			"optionalDependencies|com.acme:acme-optional|1.0.0|",
		)
	})

	t.Run("an explicitly declared ascii encoding is read", func(t *testing.T) {
		m := scanFixture(t, "pom.xml", `<?xml version="1.0" encoding="US-ASCII"?>
<project>
  <groupId>com.acme</groupId>
  <artifactId>acme-ascii</artifactId>
  <version>1.0.0</version>
</project>
`)
		assertIdentity(t, m, "com.acme:acme-ascii|1.0.0|")
	})

	t.Run("a legacy single-byte encoding is read rather than refused", func(t *testing.T) {
		m := scanFixture(t, "pom.xml", "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n"+
			"<project>\n"+
			"  <groupId>com.acme</groupId>\n"+
			"  <artifactId>acme-caf\xe9</artifactId>\n"+
			"  <version>1.0.0</version>\n"+
			"</project>\n")
		if !strings.HasPrefix(m.Name, "com.acme:acme-caf") {
			t.Fatalf("latin-1 pom name = %q", m.Name)
		}
		assertIdentity(t, m, m.Name+"|1.0.0|")
	})

	t.Run("android manifest identity", func(t *testing.T) {
		m := scanFixture(t, "app/src/main/AndroidManifest.xml", `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.acme.app"
    android:versionName="1.0.0"
    android:versionCode="42">
  <application android:label="Acme" />
</manifest>
`)
		assertIdentity(t, m, "com.acme.app|1.0.0|42")
	})
}

func TestPublicAPIScannerReadsEngineManifests(t *testing.T) {
	t.Run("unity packages and project settings", func(t *testing.T) {
		dir := writeTree(t, t.TempDir(), map[string]string{
			"Packages/manifest.json": `{
  "dependencies": {
    "com.unity.render-pipelines.universal": "14.0.9",
    "com.acme.core": "file:../../packages/core",
    "com.acme.linked": "link:../../packages/linked",
    "com.acme.git": "https://github.com/acme/core.git#v1.2.3"
  }
}
`,
			"ProjectSettings/ProjectSettings.asset": `%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!129 &1
PlayerSettings:
  m_ObjectHideFlags: 0
  productName: Acme Game#1
  bundleVersion: 1.0.0 # marketing
  AndroidBundleVersionCode: 42
  buildNumber:
    Standalone: 7
    iPhone: 9
  vectorData:
    - 1
    - 2
  # a comment line
  notAnEntry
`,
		})
		mans, err := scanner.Scan(t.Context(), dir)
		if err != nil || len(mans) != 2 {
			t.Fatalf("scan = %+v, err = %v", mans, err)
		}
		byPath := map[string]scanner.Manifest{}
		for _, m := range mans {
			byPath[m.Path] = m
		}
		assertDeps(t, byPath["Packages/manifest.json"],
			"dependencies|com.unity.render-pipelines.universal|14.0.9|",
			"dependencies|com.acme.core|file:../../packages/core|../../packages/core",
			"dependencies|com.acme.linked|link:../../packages/linked|../../packages/linked",
			"dependencies|com.acme.git|https://github.com/acme/core.git#v1.2.3|",
		)
		assertIdentity(t, byPath["ProjectSettings/ProjectSettings.asset"], "Acme Game#1|1.0.0|42")
	})

	t.Run("a unity project stamping only a per-platform counter", func(t *testing.T) {
		m := scanFixture(t, "ProjectSettings/ProjectSettings.asset", `PlayerSettings:
  productName: Acme
  bundleVersion: 2.0.0
  buildNumber:
    iPhone: 11
    Standalone: 12
`)
		assertIdentity(t, m, "Acme|2.0.0|11")
	})

	t.Run("unreal descriptors", func(t *testing.T) {
		dir := writeTree(t, t.TempDir(), map[string]string{
			"Acme.uproject": `{
	"FileVersion": 3,
	"EngineAssociation": "5.4",
	"VersionName": "9.9.9",
	"Plugins": [
		{ "Name": "AcmeNet", "Enabled": true },
		{ "Name": "AcmeAudio", "Enabled": false },
		{ "Enabled": true }
	]
}
`,
			"Plugins/AcmeNet/AcmeNet.uplugin": `{
	"FileVersion": 3,
	"Version": 7,
	"VersionName": "1.0.0",
	"Plugins": [ { "Name": "Networking" } ]
}
`,
		})
		mans, err := scanner.Scan(t.Context(), dir)
		if err != nil || len(mans) != 2 {
			t.Fatalf("scan = %+v, err = %v", mans, err)
		}
		byPath := map[string]scanner.Manifest{}
		for _, m := range mans {
			byPath[m.Path] = m
		}
		project := byPath["Acme.uproject"]
		assertIdentity(t, project, "Acme||")
		assertDeps(t, project, "dependencies|AcmeNet||", "dependencies|AcmeAudio||")
		assertDropped(t, project, "plugin 2: no name")

		plugin := byPath["Plugins/AcmeNet/AcmeNet.uplugin"]
		assertIdentity(t, plugin, "AcmeNet|1.0.0|7")
		assertDeps(t, plugin, "dependencies|Networking||")
	})

	t.Run("an unreal counter written as text or as a container", func(t *testing.T) {
		quoted := scanFixture(t, "Quoted.uplugin", `{"Version":"8","VersionName":"1.0.0"}`)
		assertIdentity(t, quoted, "Quoted|1.0.0|8")
		container := scanFixture(t, "Container.uplugin", `{"Version":{"Major":1},"VersionName":"1.0.0"}`)
		assertIdentity(t, container, "Container|1.0.0|")
		absent := scanFixture(t, "Absent.uplugin", `{"Version":null,"VersionName":"1.0.0"}`)
		assertIdentity(t, absent, "Absent|1.0.0|")
	})

	t.Run("unreal config files", func(t *testing.T) {
		dir := writeTree(t, t.TempDir(), map[string]string{
			"Config/DefaultGame.ini": `[/Script/EngineSettings.GeneralProjectSettings]
ProjectName=Acme
ProjectVersion=1.0.0 ; the marketing version
CopyrightNotice=Acme "Pro\;Max" 2026
`,
			"Config/DefaultEngine.ini": `[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]
StoreVersion=42
VersionDisplayName=1.0.0
+ExtraPath=/Game/Extra
`,
		})
		mans, err := scanner.Scan(t.Context(), dir)
		if err != nil || len(mans) != 2 {
			t.Fatalf("scan = %+v, err = %v", mans, err)
		}
		for _, m := range mans {
			if m.Version != "1.0.0" {
				t.Errorf("%s version = %q", m.Path, m.Version)
			}
		}
	})

	t.Run("godot project, plugin and export presets", func(t *testing.T) {
		project := scanFixture(t, "project.godot", `; Engine configuration file.
config_version=5

[application]

config/name="Acme \"Pro\" ; Game"
config/version="1.0.0"
=orphan
config/features=PackedStringArray("4.2")
run/main_scene="res://main.tscn"

[display]

config/name="ignored"
`)
		assertIdentity(t, project, `Acme "Pro" ; Game|1.0.0|`)

		plugin := scanFixture(t, "addons/acme/plugin.cfg", `[plugin]

name="Acme Addon"
description="Does things"
version="1.2.3"
author=Acme
script="plugin.gd"

[other]

name="ignored"
version="9.9.9"
`)
		assertIdentity(t, plugin, "Acme Addon|1.2.3|")

		presets := scanFixture(t, "export_presets.cfg", `[preset.0]

name="Android"
platform="Android"

[preset.0.options]

version/code=42
version/name="1.0.0"

[preset.1]

name="iOS"

[preset.1.options]

application/short_version="9.9.9"
`)
		assertIdentity(t, presets, "Android|1.0.0|42")
	})

	t.Run("a settings file with no trailing newline still parses", func(t *testing.T) {
		unity := scanFixture(t, "ProjectSettings/ProjectSettings.asset",
			"PlayerSettings:\n  productName: Acme\n  bundleVersion: 3.0.0")
		assertIdentity(t, unity, "Acme|3.0.0|")

		godot := scanFixture(t, "project.godot",
			"[application]\nconfig/name=\"Acme\"\nconfig/version=\"3.0.0\"")
		assertIdentity(t, godot, "Acme|3.0.0|")
	})

	t.Run("defold and o3de", func(t *testing.T) {
		defold := scanFixture(t, "game.project", `[project]
title = Acme
version = 1.0.0
dependencies#0 = https://example.com/dep.zip
`)
		if defold.Version != "1.0.0" {
			t.Errorf("defold version = %q", defold.Version)
		}

		gem := scanFixture(t, "gem.json", `{
    "gem_name": "AcmeGem",
    "version": "1.0.0",
    "dependencies": [
        "Atom_RHI==1.0.0",
        "Camera",
        "  ",
        "==1.0.0"
    ]
}
`)
		assertIdentity(t, gem, "AcmeGem|1.0.0|")
		assertDeps(t, gem,
			"dependencies|Atom_RHI|==1.0.0|",
			"dependencies|Camera||",
		)

		unrelated := scanFixture(t, "project.json", `{"someOtherTool": true}`)
		assertIdentity(t, unrelated, "||")
	})
}

func TestPublicAPIScannerReadsNodeGoAndCompose(t *testing.T) {
	t.Run("package.json local ranges", func(t *testing.T) {
		m := scanFixture(t, "package.json", `{
  "name": "@acme/app",
  "version": "1.0.0",
  "dependencies": {
    "@acme/core": "file:../core",
    "@acme/ui": "link:../ui",
    "@acme/remote": "workspace:*",
    "left-pad": "^1.3.0"
  },
  "devDependencies": { "vitest": "^2.0.0" },
  "peerDependencies": { "react": ">=18" },
  "optionalDependencies": { "fsevents": "^2.3.0" }
}
`)
		assertIdentity(t, m, "@acme/app|1.0.0|")
		assertDeps(t, m,
			"dependencies|@acme/core|file:../core|../core",
			"dependencies|@acme/ui|link:../ui|../ui",
			"dependencies|@acme/remote|workspace:*|",
			"dependencies|left-pad|^1.3.0|",
			"devDependencies|vitest|^2.0.0|",
			"peerDependencies|react|>=18|",
			"optionalDependencies|fsevents|^2.3.0|",
		)
	})

	t.Run("composer skips platform requirements", func(t *testing.T) {
		m := scanFixture(t, "composer.json", `{
  "name": "acme/app",
  "version": "1.0.0",
  "require": {
    "php": ">=8.2",
    "ext-json": "*",
    "lib-curl": "*",
    "composer-runtime-api": "^2.0",
    "acme/core": "^1.0"
  },
  "require-dev": { "phpunit/phpunit": "^11.0" }
}
`)
		assertIdentity(t, m, "acme/app|1.0.0|")
		assertDeps(t, m,
			"dependencies|acme/core|^1.0|",
			"devDependencies|phpunit/phpunit|^11.0|",
		)
	})

	t.Run("go.mod replace directives and indirect requirements", func(t *testing.T) {
		m := scanFixture(t, "go.mod", `module example.com/acme/app

go 1.26

require (
	example.com/acme/core v1.0.0
	example.com/acme/tools v0.1.0 // indirect
	example.com/acme/pinned v1.5.0 // indirect
	golang.org/x/mod v0.29.0
)

replace example.com/acme/core => ../core

replace example.com/acme/pinned => ../pinned

replace golang.org/x/mod => example.com/fork/mod v0.1.0
`)
		assertIdentity(t, m, "example.com/acme/app||")
		assertDeps(t, m,
			"dependencies|example.com/acme/core|v1.0.0|../core",
			"dependencies|example.com/acme/pinned|v1.5.0|../pinned",
			"dependencies|golang.org/x/mod|v0.29.0|",
		)
		if len(m.Indirect) != 1 || m.Indirect[0].Name != "example.com/acme/tools" {
			t.Fatalf("indirect requirements = %+v", m.Indirect)
		}
	})

	t.Run("a go.mod with an unreadable directive still yields its requirements", func(t *testing.T) {
		m := scanFixture(t, "go.mod", `module example.com/acme/lax

go 1.26

nonsense directive here

require example.com/acme/core v1.0.0
`)
		assertDeps(t, m, "dependencies|example.com/acme/core|v1.0.0|")
	})

	t.Run("compose identity and dependencies", func(t *testing.T) {
		m := scanFixture(t, "compose.yaml", `services:
  api:
    build:
      context: .
    image: ghcr.io/acme/api:1.0.0
    depends_on:
      - cache
  cache:
    image: redis:7.2
  proxy:
    image: ghcr.io/acme/api:1.0.0
  broken:
  listed:
    - not
    - a
    - mapping
`)
		assertIdentity(t, m, "ghcr.io/acme/api|1.0.0|")
		assertDeps(t, m, "dependencies|redis|7.2|")
		assertDropped(t, m,
			"service broken: not a mapping",
			"service listed: not a mapping",
		)
	})

	t.Run("dockerfile image dependencies", func(t *testing.T) {
		m := scanFixture(t, "Dockerfile", `# syntax=docker/dockerfile:1.7
FROM ghcr.io/acme/base:1.0.0 AS build
RUN --mount=type=cache,target=/root/.cache make build

FROM gcr.io/distroless/static@sha256:abc AS runtime
COPY --from=build /out /out
`)
		if len(m.Deps) == 0 {
			t.Fatalf("dockerfile declared no dependencies: %+v", m)
		}
	})
}

func TestPublicAPIScannerReportsUnreadableManifests(t *testing.T) {
	cases := []struct{ name, path, body string }{
		{"npm", "package.json", `{"name":`},
		{"composer", "composer.json", `{"require":[`},
		{"cargo", "Cargo.toml", "[package\nname = 1\n"},
		{"pyproject", "pyproject.toml", "[project\n"},
		{"gradle catalog", "libs.versions.toml", "[versions\n"},
		{"pubspec", "pubspec.yaml", "name: [unclosed\n"},
		{"compose", "compose.yaml", "services: [unclosed\n"},
		{"aqua", "aqua.yaml", "packages: [unclosed\n"},
		{"aqua not a mapping", "aqua.yaml", "- one\n- two\n"},
		{"aqua packages not a sequence", "aqua.yaml", "packages:\n  name: acme\n"},
		{"aqua package not a mapping", "aqua.yaml", "packages:\n  - just-a-string\n"},
		{"maven", "pom.xml", "<project><version>1.0.0</project>"},
		{"maven encoding", "pom.xml", `<?xml version="1.0" encoding="Shift_JIS"?><project/>`},
		{"csproj", "Acme.csproj", "<Project><ItemGroup></Project>"},
		{"nuspec", "Acme.nuspec", "<package><metadata></package>"},
		{"packages props", "Directory.Packages.props", "<Project><ItemGroup></Project>"},
		{"packages config", "packages.config", "<packages><package></packages>"},
		{"android manifest", "AndroidManifest.xml", "<manifest><application></manifest>"},
		{"android root element", "AndroidManifest.xml", "<other/>"},
		{"plist", "Info.plist", "<plist><dict><key>a</key></plist>"},
		{"plist nested skip", "Info.plist", "<plist><dict><key>a</key><string>b</string><array><b></dict></plist>"},
		{"plist key element", "Info.plist", "<plist><dict><key><b></key></dict></plist>"},
		{"plist value skip", "Info.plist", "<plist><dict><key>a</key><array><b></dict></plist>"},
		{"plist value element", "Info.plist", "<plist><dict><key>a</key><string><b></string></dict></plist>"},
		{"plist entity", "Info.plist", "<plist><dict><key>a</key><string>b</string>&bad;</dict></plist>"},
		{"plist prologue", "Info.plist", "<plist>&bad;<dict></dict></plist>"},
		{"go module", "go.mod", "module example.com/x\n\nrequire (\n"},
		{"aqua unnamed package", "aqua.yaml", "packages:\n  - version: v1.0.0\n"},
		{"unity packages", "Packages/manifest.json", `{"dependencies":`},
		{"unreal project", "Acme.uproject", `{"Plugins":`},
		{"o3de gem", "gem.json", `{"gem_name":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTree(t, t.TempDir(), map[string]string{tc.path: tc.body})
			mans, err := scanner.Scan(t.Context(), dir)
			if err == nil {
				t.Fatalf("unreadable %s parsed: %+v", tc.path, mans)
			}
			if !strings.Contains(err.Error(), tc.path) {
				t.Errorf("error does not name the manifest: %v", err)
			}
			if len(mans) != 0 {
				t.Errorf("unreadable manifest yielded %+v", mans)
			}
			// The reader never rewrites what it could not read.
			if got := readFile(t, dir+"/"+tc.path); got != tc.body {
				t.Errorf("scan changed the input: %q", got)
			}
		})
	}
}

func TestPublicAPIScannerEcosystemsCoverEveryFormat(t *testing.T) {
	for _, f := range manifest.Formats {
		if eco := scanner.EcosystemOf(f); eco == "" {
			t.Errorf("format %q has no ecosystem", f)
		}
	}
	if eco := scanner.EcosystemOf(manifest.Format("invented")); eco != "" {
		t.Errorf("unknown format reported ecosystem %q", eco)
	}
	if !errors.Is(scanner.ErrManifestTooLarge, scanner.ErrManifestTooLarge) {
		t.Fatal("the size sentinel does not match itself")
	}
}
