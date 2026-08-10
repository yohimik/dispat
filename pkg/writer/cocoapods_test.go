package writer

import (
	"os"
	"strings"
	"testing"
)

func TestPodfileRewritePreservesEveryOtherByte(t *testing.T) {
	// Both quote styles, a trailing comment, an option hash, a two-literal
	// constraint and a pod with no constraint at all.
	src := `platform :ios, '13.0'

target 'Acme' do
  pod 'Alamofire', '~> 5.6'   # http
  pod "SwiftyJSON",  "5.0.1"
  pod 'Bounded', '>= 1.0', '< 2.0'
  pod 'Core', :path => '../Core'
  pod 'Unversioned'
end
`
	path := seed(t, "Podfile", src)

	res, err := Rewrite(path, "9.9.9", []Edit{
		{Name: "Alamofire", Range: "~> 5.9"},
		{Name: "SwiftyJSON", Range: "5.0.2"},
		{Name: "Bounded", Range: "~> 1.5"},  // two literals: not spliceable
		{Name: "Core", Range: "1.0"},        // path-pinned: nothing to replace
		{Name: "Unversioned", Range: "1.0"}, // no requirement: never added
		{Name: "Ghost", Range: "1.0"},       // not declared
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`pod 'Alamofire', '~> 5.6'`, `pod 'Alamofire', '~> 5.9'`,
		`pod "SwiftyJSON",  "5.0.1"`, `pod "SwiftyJSON",  "5.0.2"`,
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	// A Podfile declares no version of its own, so the version argument has no
	// target.
	if len(res.Applied) != 2 || res.VersionWritten {
		t.Errorf("result mismatch: %+v", res)
	}
	if len(res.Missing) != 4 {
		t.Errorf("everything unspliceable must be reported missing: %+v", res.Missing)
	}
}

func TestPodfileRewriteNoChangeLeavesFileAlone(t *testing.T) {
	src := "target 'Acme' do\n  pod 'Alamofire', '~> 5.6'\nend\n"
	path := seed(t, "Podfile", src)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Rewrite(path, "", []Edit{{Name: "Alamofire", Range: "~> 5.6"}})
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

func TestPodfileRewriteRefusesLiteralBreakingText(t *testing.T) {
	// A quote, a backslash or a '#' would end the literal early or open an
	// interpolation, turning a version bump into a syntax change.
	src := "target 'Acme' do\n  pod 'Alamofire', '~> 5.6'\nend\n"
	for _, bad := range []string{`5.9'`, `5.9"`, `5.9\`, `#{x}`} {
		path := seed(t, "Podfile", src)
		if _, err := Rewrite(path, "", []Edit{{Name: "Alamofire", Range: bad}}); err == nil {
			t.Errorf("range %q must be refused", bad)
		}
		if read(t, path) != src {
			t.Errorf("a refused rewrite modified the file for %q", bad)
		}
	}
}

func TestPodspecRewriteVersionAndDependencies(t *testing.T) {
	src := `Pod::Spec.new do |s|
  s.name     = 'AFNetworking'
  s.version  = '4.0.1'
  s.source   = { :git => 'https://example.com/a.git', :tag => s.version }

  s.dependency 'Bolts', '~> 1.9'

  s.subspec 'NSURLSession' do |ss|
    ss.version = '0.0.1'
    ss.ios.dependency 'AFNetworking/Reachability', '~> 1.0'
  end
end
`
	path := seed(t, "AFNetworking.podspec", src)

	res, err := Rewrite(path, "5.0.0", []Edit{
		{Name: "Bolts", Range: "~> 2.0"},
		{Name: "AFNetworking/Reachability", Range: "~> 2.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`s.version  = '4.0.1'`, `s.version  = '5.0.0'`,
		`'Bolts', '~> 1.9'`, `'Bolts', '~> 2.0'`,
		`'AFNetworking/Reachability', '~> 1.0'`, `'AFNetworking/Reachability', '~> 2.0'`,
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	// Only the first version assignment is the spec's own; the subspec's stays.
	if !strings.Contains(read(t, path), `ss.version = '0.0.1'`) {
		t.Error("a subspec's version must not be treated as the spec's own")
	}
	if !res.VersionWritten || len(res.Applied) != 2 || len(res.Missing) != 0 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestPodspecRewriteLeavesComputedVersionAlone(t *testing.T) {
	src := "Pod::Spec.new do |s|\n  s.name = 'Acme'\n  s.version = Acme::VERSION\nend\n"
	path := seed(t, "Acme.podspec", src)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten || read(t, path) != src {
		t.Errorf("a computed version must not be replaced with a literal: %+v", res)
	}
}

func TestRewriteDispatchCoversMobileManifests(t *testing.T) {
	for _, name := range []string{
		"Info.plist", "AndroidManifest.xml", "libs.versions.toml",
		"project.pbxproj", "Podfile", "Alamofire.podspec",
		"app/build.gradle", "app/build.gradle.kts",
	} {
		if !Supported(name) {
			t.Errorf("%s should have a writer", name)
		}
	}
	// The suffix and exact-name rules must not over-match: a file that merely
	// contains a manifest's name is not one.
	for _, name := range []string{"Podfilex", "notes.podspec.txt", "settings.gradle", "Gemfile.lock"} {
		if Supported(name) {
			t.Errorf("%s should not have a writer", name)
		}
	}
}

func TestPodfileRewriteSeparatesChangedFromAlreadyCorrect(t *testing.T) {
	// A pod declared in both an app target and its test target is spliced in
	// both places but is one edit, so it is reported once. The pod that was
	// already at the wanted requirement is neither applied nor missing.
	src := `target 'Acme' do
  pod 'Shared', '~> 1.0'
  pod 'Stays', '9.9.9'

  target 'AcmeTests' do
    pod 'Shared', '~> 1.0'
  end
end
`
	path := seed(t, "Podfile", src)
	res, err := Rewrite(path, "", []Edit{
		{Name: "Shared", Range: "~> 2.0"},
		{Name: "Stays", Range: "9.9.9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(read(t, path), "'~> 2.0'") != 2 {
		t.Errorf("both declarations must be spliced: %q", read(t, path))
	}
	if len(res.Applied) != 1 || res.Applied[0].Name != "Shared" {
		t.Errorf("one edit, reported once: %+v", res.Applied)
	}
	if len(res.Missing) != 0 {
		t.Errorf("an already-correct declaration is not missing: %+v", res.Missing)
	}
}
