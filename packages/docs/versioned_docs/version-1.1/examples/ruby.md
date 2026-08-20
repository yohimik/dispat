# Ruby gems

Gems in one repository, built and pushed to RubyGems, including the common case where the version lives in a
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

Two things here are Ruby-shaped.

**`range: "~> {version}"`.** The keywords `caret` and `tilde` spell npm's operators. Ruby writes pessimistic
constraints as `~> 1.3.0`, so the policy is given as a template and the run fills in `{version}`.

**The `replace` rule.** A gemspec that says `s.version = Acme::Core::VERSION` hands the number to Ruby, and dispat
will not overwrite a constant with a literal: the indirection exists on purpose. The rule writes the constant instead,
in whichever `version.rb` the glob finds, which is where every generated gem keeps it.

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

Two packages, two different mechanisms, one pass. `file reconciled` is the replace rule; `manifest reconciled` is the
parsing strategy. The results:

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

`acme-core` carries no `@version` on its identity line: its gemspec defers to `Acme::Core::VERSION`, and the reader
reports what the file says rather than following the constant.

- **A gemspec** contributes its `name`, its `version` when that version is a literal string, and its
  `add_dependency`, `add_runtime_dependency` and `add_development_dependency` calls. A version taken from a constant
  is read as absent rather than guessed at.
- **A Gemfile** declares no identity of its own; it is a list of requirements. A `gem` inside a `:development` or
  `:test` group becomes a dev dependency.
- **Git and path pins are left alone.** `gem "acme-core", path: "../core"` names a place, not a version, and
  overwriting it would break the local setup it exists for.

## Worth knowing

- **`gem push` cannot be undone.** `gem yank` removes a version from the index but never frees the number.
- **The gem file name contains the version**, which is why the publish script above builds it out of
  `$DISPAT_NEW_VERSION` rather than globbing. A glob would happily push last week's build.
- **`Gemfile.lock` follows the manifests.** Add `bundle lock` as a [`syncLock`](../configuration/autoversion.md)
  script if a deployable application in the repository commits its lock.
- **Credentials go in `~/.gem/credentials` or `GEM_HOST_API_KEY`.** The [`flow.login` slot](./login.md) is the place
  to write that file once per space rather than once per gem.

## See also

- [The replacer](../editing/replacer.md) for the find-and-write strategy in full.
- [autoVersion](../configuration/autoversion.md) for `range` templates and the two strategies together.
- [An iOS app and a CocoaPods library](./apple.md) for the other ecosystem built on Ruby files.
