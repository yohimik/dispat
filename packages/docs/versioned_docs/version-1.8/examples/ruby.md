# Ruby gems

You can manage gems in one repository and build and push them to RubyGems. This works even when the version lives in a
`version.rb` constant rather than in the gemspec.

## The layout

```
gems/core/acme-core.gemspec           version comes from Acme::Core::VERSION
gems/core/lib/acme/core/version.rb    where that constant lives
gems/web/acme-web.gemspec             declares its version inline, depends on acme-core
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "scripts": {
    "build": "gem build *.gemspec",
    "publish": "gem push acme-$DISPAT_PACKAGE-$DISPAT_NEW_VERSION.gem"
  },
  "spaces": {
    "gems": {
      "path": "gems",
      "flow": {"build": "build", "publish": "publish"},
      "autoVersion": {
        "enabled": true,
        "range": "~> {version}",
        "replace": [
          {"files": ["lib/**/version.rb"], "find": "VERSION = \"{previous}\"", "write": "VERSION = \"{version}\""}
        ]
      }
    }
  }
}
```

Two settings handle Ruby conventions.

**`range: "~> {version}"`.** The keywords `caret` and `tilde` map to npm operators. Ruby writes pessimistic constraints
as `~> 1.3.0`, so you provide the policy as a template and dispat fills in `{version}`.

**The `replace` rule.** A gemspec that says `s.version = Acme::Core::VERSION` hands the number to Ruby, and dispat will
not overwrite a constant with a literal because the indirection exists on purpose. The rule writes the constant
instead. It targets whichever `version.rb` the glob finds.

## A release

```console
$ git commit -m "feat(core)^: add the middleware stack"
$ dispat status
12:45:10 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=gems version="1.2.0 -> 1.3.0"
12:45:10 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=web reason="propagated from core" space=gems version="0.4.1 -> 0.4.2"
12:45:10 INF release plan ready held=0 packages=2 releasing=2

$ dispat autoversion
12:45:10 INF file reconciled file=lib/acme/core/version.rb occurrences=1 package=core stage=version
12:45:10 INF manifest reconciled manifest=acme-web.gemspec package=web ranges=1 stage=version versionWritten=true
12:45:10 INF auto-versioning finished failed=0 ran=2 skipped=0 stage=autoversion
```

dispat updates two packages using two different mechanisms in one pass. The `file reconciled` output shows the replace
rule in action, and the `manifest reconciled` output shows the parsing strategy. The command produces these results:

```ruby
module Acme
  module Core
    VERSION = "1.3.0"
  end
end
```

```ruby
Gem::Specification.new do |s|
  s.name    = "acme-web"
  s.version = "0.4.2"
  s.summary = "The web front end"

  s.add_dependency "acme-core", "~> 1.3.0"
  s.add_development_dependency "rspec", "~> 3.13"
end
```

## What dispat reads and writes

```console
$ dispat scanner
Gemfile  rubygems
  dependencies     acme-core  ~> 1.2.0
  devDependencies  rspec      ~> 3.13
gems/core/acme-core.gemspec  rubygems  acme-core
gems/web/acme-web.gemspec  rubygems  acme-web@0.4.1
  dependencies     acme-core  ~> 1.2.0
  devDependencies  rspec      ~> 3.13
3 manifest(s), 4 dependency declaration(s)
```

The `acme-core` package carries no `@version` on its identity line. Its gemspec defers to `Acme::Core::VERSION`. The
reader reports what the file says rather than following the constant.

- **A gemspec** contributes its `name`, its `version` when that version is a literal string, and its `add_dependency`,
  `add_runtime_dependency` and `add_development_dependency` calls. dispat reads a version taken from a constant as
  absent rather than guessing at it.
- **A Gemfile** declares no identity of its own. It acts as a list of requirements. A `gem` inside a `:development` or
  `:test` group becomes a dev dependency.
- **Git and path pins are left alone.** The declaration `gem "acme-core", path: "../core"` names a place instead of a
  version. Overwriting it would break the local setup.

## Worth knowing

- **`gem push` cannot be undone.** The `gem yank` command removes a version from the index, but it never frees the
  number.
- **The gem file name contains the version**, so the publish script builds the filename out of `$DISPAT_NEW_VERSION`
  rather than globbing. A glob would happily push last week's build.
- **`Gemfile.lock` follows the manifests.** Add `bundle lock` as a [`syncLock`](../configuration/autoversion.md) script
  if your deployable application commits its lockfile.
- **Credentials go in `~/.gem/credentials` or `GEM_HOST_API_KEY`.** Write that file once per space using the
  [`flow.login` slot](./login.md). This saves you from authenticating once per gem.

## See also

- [The replacer](../editing/replacer.md) explains the find-and-write strategy in full.
- [autoVersion](../configuration/autoversion.md) covers `range` templates and the two strategies together.
- [An iOS app and a CocoaPods library](./apple.md) shows the other ecosystem built on Ruby files.
