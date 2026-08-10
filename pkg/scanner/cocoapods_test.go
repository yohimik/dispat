package scanner

import (
	"reflect"
	"testing"
)

func TestPodfileTargetsOptionsAndRequirements(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Podfile", `platform :ios, '13.0'
source 'https://cdn.cocoapods.org/'

target 'Acme' do
  use_frameworks!
  pod 'Alamofire', '~> 5.6'
  pod 'Bounded', '>= 1.0', '< 2.0'
  pod 'Core', :path => '../Core'
  pod 'Modern', path: '../Modern'
  pod 'Pinned', :git => 'https://example.com/p.git', :tag => '1.2.3'
  pod 'Unversioned'

  target 'AcmeTests' do
    inherit! :search_paths
    pod 'Quick', '~> 7.0'
  end
end
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "Podfile", Ecosystem: EcosystemCocoaPods, Root: true,
		Deps: []DeclaredDep{
			{Name: "Alamofire", Range: "~> 5.6", Kind: KindDependencies},
			{Name: "Bounded", Range: ">= 1.0, < 2.0", Kind: KindDependencies},
			{Name: "Core", Kind: KindDependencies, LocalPath: "../Core"},
			{Name: "Modern", Kind: KindDependencies, LocalPath: "../Modern"},
			// A git-pinned pod has no version text to write; the name still
			// carries the graph.
			{Name: "Pinned", Kind: KindDependencies},
			{Name: "Unversioned", Kind: KindDependencies},
			{Name: "Quick", Range: "~> 7.0", Kind: KindDevDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestPodfileDropsInterpolatedPaths(t *testing.T) {
	// Taken from React Native's own Podfile: an interpolated path names a Ruby
	// variable, and recording it would send ResolveLocalDir chasing a folder
	// that does not exist.
	dir := t.TempDir()
	write(t, dir, "Podfile", `target 'RNTester' do
  pod 'ReactCommon-Samples', :path => "#{@prefix_path}/ReactCommon/react/nativemodule/samples"
  pod 'React-RCTTest', :path => "./RCTTest"
  pod 'OCMock', '~> 3.9.1'
end
`)
	m := scanOne(t, dir)
	want := []DeclaredDep{
		{Name: "OCMock", Range: "~> 3.9.1", Kind: KindDependencies},
		{Name: "React-RCTTest", Kind: KindDependencies, LocalPath: "./RCTTest"},
		{Name: "ReactCommon-Samples", Kind: KindDependencies},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps mismatch:\n got %+v\nwant %+v", m.Deps, want)
	}
}

func TestPodfileIgnoresCommentsAndSurroundingRuby(t *testing.T) {
	// A real Podfile branches, shells out and defines methods. None of it may
	// produce a declaration, and a commented-out pod is not a dependency.
	dir := t.TempDir()
	write(t, dir, "Podfile", `require_relative '../scripts/pods'

cmake_path = `+"`command -v cmake`"+`
if cmake_path == ""
  Pod::UI.puts "no cmake"
end

# pod 'Commented', '1.0'
def pods(target_name, options = {})
  pod 'Shared', '1.0' # the real one
end

post_install do |installer|
  react_native_post_install(installer)
end
`)
	m := scanOne(t, dir)
	want := []DeclaredDep{{Name: "Shared", Range: "1.0", Kind: KindDependencies}}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps mismatch:\n got %+v\nwant %+v", m.Deps, want)
	}
}

func TestPodspecIdentityAndDependencies(t *testing.T) {
	// Modelled on AFNetworking's real podspec: aligned assignments, subspec
	// blocks with their own block parameter, and platform-scoped dependencies.
	dir := t.TempDir()
	write(t, dir, "AFNetworking.podspec", `Pod::Spec.new do |s|
  s.name     = 'AFNetworking'
  s.version  = '4.0.1'
  s.license  = 'MIT'
  s.source   = { :git => 'https://github.com/AFNetworking/AFNetworking.git', :tag => s.version }

  s.ios.deployment_target = '9.0'
  s.ios.pod_target_xcconfig = { 'PRODUCT_BUNDLE_IDENTIFIER' => 'com.alamofire.AFNetworking' }

  s.dependency 'Bolts', '~> 1.9'

  s.subspec 'NSURLSession' do |ss|
    ss.dependency 'AFNetworking/Serialization'
    ss.ios.dependency 'AFNetworking/Reachability'
  end
end
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "AFNetworking.podspec", Ecosystem: EcosystemCocoaPods,
		Name: "AFNetworking", Version: "4.0.1", Root: true,
		Deps: []DeclaredDep{
			{Name: "AFNetworking/Reachability", Kind: KindDependencies},
			{Name: "AFNetworking/Serialization", Kind: KindDependencies},
			{Name: "Bolts", Range: "~> 1.9", Kind: KindDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestPodspecNonLiteralVersionYieldsNothing(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Acme.podspec", `Pod::Spec.new do |s|
  s.name = 'Acme'
  s.version = Acme::VERSION
end
`)
	m := scanOne(t, dir)
	if m.Name != "Acme" || m.Version != "" {
		t.Errorf("a constant is not a version literal: %+v", m)
	}
}
