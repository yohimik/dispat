package scanner

import (
	"reflect"
	"testing"
)

func TestGemfileGroupsOptionsAndRequirements(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Gemfile", `source 'https://rubygems.org'
git_source(:github) { |repo| "https://github.com/#{repo}.git" }

ruby '3.2.2'

gem 'rails', '~> 7.0.4'
gem 'pg', '>= 0.18', '< 2.0'
gem 'local', path: '../local'
gem 'pinned', git: 'https://example.com/p.git', tag: 'v1.0'
gem 'inline_dev', '~> 1.0', group: :development

group :development, :test do
  gem 'rspec-rails', '~> 6.0'
end

group :production do
  gem 'unicorn'
end
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "Gemfile", Ecosystem: EcosystemRubyGems, Root: true,
		Deps: []DeclaredDep{
			{Name: "local", Kind: KindDependencies, LocalPath: "../local"},
			{Name: "pg", Range: ">= 0.18, < 2.0", Kind: KindDependencies},
			// A git-pinned gem has no version text; the name still carries the graph.
			{Name: "pinned", Kind: KindDependencies},
			{Name: "rails", Range: "~> 7.0.4", Kind: KindDependencies},
			// :production is not a development group.
			{Name: "unicorn", Kind: KindDependencies},
			{Name: "inline_dev", Range: "~> 1.0", Kind: KindDevDependencies},
			{Name: "rspec-rails", Range: "~> 6.0", Kind: KindDevDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestGemspecIdentityAndDependencyMethods(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "acme.gemspec", `# frozen_string_literal: true

require_relative "lib/acme/version"

Gem::Specification.new do |spec|
  spec.name          = "acme"
  spec.version       = "1.2.3"
  spec.authors       = ["Me"]
  spec.summary       = "A gem"
  spec.files         = Dir["lib/**/*"]

  spec.add_dependency "rails", "~> 7.0"
  spec.add_runtime_dependency "pg", ">= 1.0"
  spec.add_development_dependency "rspec", "~> 3.0"
end
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "acme.gemspec", Ecosystem: EcosystemRubyGems,
		Name: "acme", Version: "1.2.3", Root: true,
		Deps: []DeclaredDep{
			{Name: "pg", Range: ">= 1.0", Kind: KindDependencies},
			{Name: "rails", Range: "~> 7.0", Kind: KindDependencies},
			{Name: "rspec", Range: "~> 3.0", Kind: KindDevDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestGemspecConstantVersionYieldsNothing(t *testing.T) {
	// The overwhelmingly common shape: the version lives in a Ruby source file
	// so the library and its packaging cannot disagree.
	dir := t.TempDir()
	write(t, dir, "acme.gemspec", `Gem::Specification.new do |spec|
  spec.name    = "acme"
  spec.version = Acme::VERSION
end
`)
	m := scanOne(t, dir)
	if m.Name != "acme" || m.Version != "" {
		t.Errorf("a constant is not a version literal: %+v", m)
	}
}
