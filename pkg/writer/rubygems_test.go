package writer

import (
	"strings"
	"testing"
)

func TestGemfileRewritePreservesEveryOtherByte(t *testing.T) {
	src := `source 'https://rubygems.org'

gem 'rails', '~> 7.0.4'   # the framework
gem "pg",  ">= 1.0"
gem 'pinned', git: 'https://example.com/p.git', tag: 'v1.0'
gem 'unversioned'
gem 'bounded', '>= 1.0', '< 2.0'

group :development, :test do
  gem 'rails', '~> 7.0.4'
end
`
	path := seed(t, "Gemfile", src)

	res, err := Rewrite(path, "9.9.9", []Edit{
		{Name: "rails", Range: "~> 7.1.0"},
		{Name: "pg", Range: ">= 1.5"},
		{Name: "pinned", Range: "1.0"},
		{Name: "unversioned", Range: "1.0"},
		{Name: "bounded", Range: "~> 1.5"},
		{Name: "ghost", Range: "1.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`gem 'rails', '~> 7.0.4'   # the framework`, `gem 'rails', '~> 7.1.0'   # the framework`,
		"  gem 'rails', '~> 7.0.4'\n", "  gem 'rails', '~> 7.1.0'\n",
		`gem "pg",  ">= 1.0"`, `gem "pg",  ">= 1.5"`,
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	// rails is declared twice and spliced twice, but is one edit.
	if len(res.Applied) != 2 || res.VersionWritten {
		t.Errorf("result mismatch: %+v", res)
	}
	// Only the undeclared gem is missing; the git-pinned, unversioned and
	// two-literal gems are declared and skipped.
	if len(res.Skipped) != 3 || len(res.Missing) != 1 || res.Missing[0].Name != "ghost" {
		t.Errorf("skipped/missing split wrong: skipped=%+v missing=%+v", res.Skipped, res.Missing)
	}
}

func TestGemspecRewriteVersionAndDependencies(t *testing.T) {
	src := `Gem::Specification.new do |spec|
  spec.name          = "acme"
  spec.version       = "1.2.3"

  spec.add_dependency "rails", "~> 7.0"
  spec.add_runtime_dependency "pg", ">= 1.0"
  spec.add_development_dependency "rspec", "~> 3.0"
end
`
	path := seed(t, "acme.gemspec", src)
	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "rails", Range: "~> 7.1"},
		{Name: "rspec", Range: "~> 3.13"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`spec.version       = "1.2.3"`, `spec.version       = "2.0.0"`,
		`"rails", "~> 7.0"`, `"rails", "~> 7.1"`,
		`"rspec", "~> 3.0"`, `"rspec", "~> 3.13"`,
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if !res.VersionWritten || len(res.Applied) != 2 || len(res.Missing) != 0 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestGemspecRewriteLeavesConstantVersionAlone(t *testing.T) {
	src := "Gem::Specification.new do |spec|\n  spec.name = \"acme\"\n  spec.version = Acme::VERSION\nend\n"
	path := seed(t, "acme.gemspec", src)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten || read(t, path) != src {
		t.Errorf("a constant version must not be replaced with a literal: %+v", res)
	}
}
