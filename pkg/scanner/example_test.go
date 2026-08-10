package scanner_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yohimik/dispat/pkg/scanner"
)

func Example() {
	dir, _ := os.MkdirTemp("", "scanner-example-")
	defer os.RemoveAll(dir)
	manifest := []byte(`{
  "name": "@acme/web",
  "version": "1.2.0",
  "dependencies": {"@acme/core": "workspace:*"},
  "devDependencies": {"typescript": "^5.4.0"}
}`)
	_ = os.WriteFile(filepath.Join(dir, "package.json"), manifest, 0o644)

	mans, err := scanner.New().Scan(context.Background(), dir)
	if err != nil {
		fmt.Println("partial scan:", err)
	}
	for _, m := range mans {
		fmt.Printf("%s %s@%s\n", m.Ecosystem, m.Name, m.Version)
		for _, d := range m.Deps {
			fmt.Printf("  %s %s %q\n", d.Kind, d.Name, d.Range)
		}
	}
	// Output:
	// npm @acme/web@1.2.0
	//   dependencies @acme/core "workspace:*"
	//   devDependencies typescript "^5.4.0"
}

// Example_iOS reads an iOS application's manifests: the bundle metadata that
// carries its identity and version, and the Podfile that carries its
// dependencies.
func Example_iOS() {
	dir, _ := os.MkdirTemp("", "scanner-ios-")
	defer os.RemoveAll(dir)
	_ = os.WriteFile(filepath.Join(dir, "Info.plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key>
  <string>com.acme.app</string>
  <key>CFBundleShortVersionString</key>
  <string>1.2.3</string>
  <key>CFBundleVersion</key>
  <string>42</string>
</dict>
</plist>`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "Podfile"), []byte(`target 'Acme' do
  pod 'Alamofire', '~> 5.6'
  pod 'Core', :path => '../Core'

  target 'AcmeTests' do
    pod 'Quick', '~> 7.0'
  end
end`), 0o644)

	mans, err := scanner.New().Scan(context.Background(), dir)
	if err != nil {
		fmt.Println("partial scan:", err)
	}
	for _, m := range mans {
		fmt.Printf("%s (%s)\n", m.Path, m.Ecosystem)
		if m.Name != "" {
			fmt.Printf("  %s version=%s build=%s\n", m.Name, m.Version, m.BuildNumber)
		}
		for _, d := range m.Deps {
			fmt.Printf("  %s %s %q", d.Kind, d.Name, d.Range)
			if d.LocalPath != "" {
				fmt.Printf(" -> %s", d.LocalPath)
			}
			fmt.Println()
		}
	}
	// Output:
	// Info.plist (plist)
	//   com.acme.app version=1.2.3 build=42
	// Podfile (cocoapods)
	//   dependencies Alamofire "~> 5.6"
	//   dependencies Core "" -> ../Core
	//   devDependencies Quick "~> 7.0"
}

// Example_android reads an Android module's build script and the version
// catalog it resolves its dependency versions through. Both name libraries by
// Maven coordinate, so a catalog entry and a pom.xml dependency describe the
// same package.
func Example_android() {
	dir, _ := os.MkdirTemp("", "scanner-android-")
	defer os.RemoveAll(dir)
	_ = os.WriteFile(filepath.Join(dir, "build.gradle"), []byte(`android {
    defaultConfig {
        applicationId "com.acme.app"
        versionCode 42
        versionName "1.2.3"
    }
}

dependencies {
    implementation 'androidx.core:core-ktx:1.12.0'
    implementation project(':core')
    testImplementation 'junit:junit:4.13.2'
}`), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "gradle"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "gradle", "libs.versions.toml"), []byte(`[versions]
retrofit = "2.9.0"

[libraries]
retrofit = { module = "com.squareup.retrofit2:retrofit", version.ref = "retrofit" }`), 0o644)

	mans, err := scanner.New().Scan(context.Background(), dir)
	if err != nil {
		fmt.Println("partial scan:", err)
	}
	for _, m := range mans {
		fmt.Printf("%s (%s)\n", m.Path, m.Ecosystem)
		if m.Name != "" {
			fmt.Printf("  %s version=%s build=%s\n", m.Name, m.Version, m.BuildNumber)
		}
		for _, d := range m.Deps {
			fmt.Printf("  %s %s %q\n", d.Kind, d.Name, d.Range)
		}
	}
	// Output:
	// build.gradle (gradle)
	//   com.acme.app version=1.2.3 build=42
	//   dependencies androidx.core:core-ktx "1.12.0"
	//   dependencies core ""
	//   devDependencies junit:junit "4.13.2"
	// gradle/libs.versions.toml (gradle)
	//   dependencies com.squareup.retrofit2:retrofit "2.9.0"
}
