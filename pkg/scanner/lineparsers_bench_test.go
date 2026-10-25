package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// lineScannedCase is one parser that reads its manifest line by line, with a
// file that declares every field the parser keeps near the top and then runs
// on for megabytes, the way a real project.pbxproj does.
type lineScannedCase struct {
	name   string
	rel    string
	head   string
	filler string
	parse  parseFunc
}

var lineScannedCases = []lineScannedCase{
	{
		name: "xcodeproj",
		rel:  "App.xcodeproj/project.pbxproj",
		head: "// !$*UTF8*$!\n{\n\tbuildSettings = {\n" +
			"\t\tPRODUCT_BUNDLE_IDENTIFIER = com.example.app;\n" +
			"\t\tMARKETING_VERSION = 1.2.3;\n" +
			"\t\tCURRENT_PROJECT_VERSION = 42;\n",
		filler: "\t\tOTHER_SETTING_%d = \"a value that is none of the three settings read\";\n",
		parse:  parseXcodeProj,
	},
	{
		name: "gradle",
		rel:  "app/build.gradle",
		head: "android {\n  namespace \"com.example.ns\"\n  defaultConfig {\n" +
			"    applicationId \"com.example.app\"\n    versionName \"1.2.3\"\n    versionCode 42\n  }\n}\n" +
			"dependencies {\n  implementation 'com.squareup.retrofit2:retrofit:2.9.0'\n" +
			"  implementation project(':core')\n}\n",
		filler: "// filler comment line %d that declares nothing at all\n",
		parse:  parseGradleBuild,
	},
	{
		name:   "dockerfile",
		rel:    "Dockerfile",
		head:   "FROM golang:1.25-alpine AS build\nFROM alpine:3.20\n",
		filler: "RUN echo filler line %d that declares nothing at all\n",
		parse:  parseDockerfile,
	},
	{
		name:   "gemfile",
		rel:    "Gemfile",
		head:   "gem 'rails', '~> 7.1'\ngem 'core', path: '../core'\n",
		filler: "# filler comment line %d that declares nothing at all\n",
		parse:  parseGemfile,
	},
	{
		name: "gemspec",
		rel:  "acme.gemspec",
		head: "Gem::Specification.new do |spec|\n  spec.name = 'acme'\n  spec.version = '1.2.3'\n" +
			"  spec.add_dependency 'rack', '>= 2.0'\nend\n",
		filler: "# filler comment line %d that declares nothing at all\n",
		parse:  parseGemspec,
	},
	{
		name: "podspec",
		rel:  "Acme.podspec",
		head: "Pod::Spec.new do |s|\n  s.name = 'Acme'\n  s.version = '1.0.0'\n" +
			"  s.dependency 'Alamofire', '~> 5.0'\nend\n",
		filler: "# filler comment line %d that declares nothing at all\n",
		parse:  parsePodspec,
	},
	{
		name:   "podfile",
		rel:    "Podfile",
		head:   "target 'App' do\n  pod 'Alamofire', '~> 5.0'\n  pod 'Core', :path => '../Core'\nend\n",
		filler: "# filler comment line %d that declares nothing at all\n",
		parse:  parsePodfile,
	},
	{
		name:   "requirements",
		rel:    "requirements.txt",
		head:   "requests==2.31.0\n-e ./core\n",
		filler: "# filler comment line %d that declares nothing at all\n",
		parse:  parseRequirements,
	},
}

// build renders the case's file at roughly size bytes.
func (c lineScannedCase) build(size int) []byte {
	var b strings.Builder
	b.Grow(size + len(c.head))
	b.WriteString(c.head)
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, c.filler, i)
	}
	return []byte(b.String())
}

// liveHeapBytes is the heap still reachable after a full collection.
func liveHeapBytes() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// parseRetaining parses a freshly built file the caller never sees and
// reports how much of the heap the returned manifest keeps alive once the
// file's own bytes are garbage.
func (c lineScannedCase) parseRetaining(size int) (Manifest, int64, error) {
	before := liveHeapBytes()
	m, err := c.parse(c.rel, c.build(size))
	after := liveHeapBytes()
	return m, int64(after) - int64(before), err
}

func BenchmarkLineScannedParsers(b *testing.B) {
	const size = 1 << 20
	for _, c := range lineScannedCases {
		b.Run(c.name, func(b *testing.B) {
			data := c.build(size)
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.parse(c.rel, data); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			_, retained, err := c.parseRetaining(size)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(retained), "retained-B")
		})
	}
}

func BenchmarkReadManifest(b *testing.B) {
	for _, size := range []int{4 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "project.pbxproj")
			if err := os.WriteFile(path, lineScannedCases[0].build(size), 0o644); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for i := 0; i < b.N; i++ {
				if _, err := readManifest(path); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
