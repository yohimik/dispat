# Changelog

## pkg/config/v1.0.0 (2026-08-31)

### Breaking Changes

- initial release ([d50cd43](https://github.com/yohimik/dispat/commit/d50cd439499c93f6f979b74e8d1d03ffadbaf3f3)) (by yohimik, Claude Fable 5)
  pkg/config is dispat's config module as a standalone library: json, yaml and
  toml into one tree, `$ref` composition with the merge semantics dispat pins,
  an upward root ascent the caller classifies, a reflection-free decoder that
  keeps a key's written case and refuses both unknown keys and two spellings of
  one name, env layer helpers, and ref-aware format-preserving edits.

  The ported code carries two changes it could not carry inside dispat. The
  tree is built by the ref walk rather than copied a second time to convert a
  yaml mapping with a non-string key, so a load is one deep copy instead of
  two; and the settings map, which is where an empty object stops being a key
  and a dotted key becomes the levels it names, is one recursive walk rather
  than a flatten into delimited paths and a rebuild from them.

  The format table is the whole format story: an entry under the empty
  extension claims every file the others leave, which is where a program puts
  its own wording for a file it has no parser for.

  The go.work entry and both Dockerfiles' dependency COPY ride along, because
  a module the workspace does not use and the gates cannot download breaks
  every gate at once.

### Features

- a map setter for values of any shape ([3d63cb8](https://github.com/yohimik/dispat/commit/3d63cb8c6de82728dc5485409685d968641a2281)) (by yohimik, Claude Fable 5)
  MapOf carries the object rules — the value has to be an object, no two of
  its keys may fold together, its keys are visited in order — and takes the
  caller's own reader for each value. StringMap is now this with WeakString
  filled in.

  It is the setter to write a setter with. A map whose values are neither a
  scalar nor an object — a list with a shorthand of its own, a type with its
  own normaliser — used to mean reimplementing the rules beside the reader,
  and a reimplementation is where the fold-duplicate refusal quietly goes
  missing from one map and not the others.

  The reader is called for every key, one holding nothing included: a map of
  named values has no "this entry said nothing" the way an object's key does,
  and what an absent value means belongs to the reader.

- a watch package callers opt into ([3e37e78](https://github.com/yohimik/dispat/commit/3e37e784e2ba84794e47aaca7b885de66d6f6cee)) (by yohimik, Claude Fable 5)
  Reloading is a subpackage because it is the only thing here that needs
  fsnotify, and a program that reads its configuration once at startup should
  not link a filesystem-notification library to do it. dispat is one of those
  programs, which is also what keeps the TinyGo build clear of it.

  It watches directories, not files. A config file is usually replaced rather
  than written in place — a temp file and a rename, which is how this
  repository's own editor avoids leaving a half-written config behind — and a
  watch on the file itself follows the old inode into the void.

  The watch set is whatever the load reported, so a configuration composed
  through $ref watches every fragment, and a reload that changes which
  fragments are involved moves the watches with it.

  One goroutine, one debounce timer, two ways out: the context and Close, with
  a sync.Once between them so closing twice is a no-op rather than a panic.
  Done() is closed last, which is what a test waits on. A reload that fails
  keeps the last good value: a configuration that stops parsing is a reason to
  go on running with the one that did.

- process env binds into the tree on request ([bcd040e](https://github.com/yohimik/dispat/commit/bcd040eb3ee4c95cef5c743e6a3533b2791654a0)) (by yohimik, Claude Fable 5)
  Opt-in and closed: a binding names the keys it will accept, and a variable
  that answers to none of them either says nothing or is a typo, depending on
  Strict. That is the whole difference from the automatic binding a config
  library usually offers, where any variable at all may or may not have set
  something and nobody can tell which.

  The derivation runs one way, from a declared key to a variable name, and
  never the other. Splitting a variable name back into key levels is where an
  automatic binding has to guess whether LOG_LEVEL is `log.level` or
  `logLevel`, and the guess is wrong for somebody; here the key is given, so
  the name it derives is a fact, and the key lands in the overrides spelled
  exactly as the caller declared it — which is what lets it replace the file's
  own spelling rather than collide with it.

  A variable set to the empty string is a value. Unsetting a variable and
  setting it to nothing are two different things a deployment does on purpose.

  Two keys deriving one variable name is refused: which of them the variable
  set would otherwise be whichever the map handed over first.

- a logger the caller brings, found on the context ([2df44f0](https://github.com/yohimik/dispat/commit/2df44f0282805edefdfa90a7e6e9c33e17df2ef4)) (by yohimik, Claude Fable 5)
  A configuration loader is a thing that goes quiet and then, one day, loads
  the wrong file. The events are what someone reads at that point: which
  directories the ascent tried and what it made of each, which files a $ref
  pulled in, how many overrides landed, which key an edit was written to.

  The interface is two methods and no dependency, because a library that picks
  a logging package picks it for every program that imports it. Options.Logger
  is the one a program with a single logger sets; WithLogger puts one on the
  context for the programs that have several. Neither is required — the zero
  value is a logger that is never enabled and records nothing.

  Every emit is guarded by Enabled and the Field constructors keep scalars in
  typed slots, so a trace event nobody asked for costs an interface call and a
  comparison rather than a boxed value per field.

  The field constructors are Str/Num/Flag/Err/Any rather than Str/Int/Bool: Int
  and Bool are already the setters that fill a whole-number and a boolean
  config field, and one name for two things is a package whose examples cannot
  be copied.

- keep map keys as the file writes them ([cc43907](https://github.com/yohimik/dispat/commit/cc439077f061788b4d4fb5e6b5b7d63f45f1719e)) (by yohimik, Claude Fable 5)
  Every map key a config file writes now reaches the model spelled as its
  author wrote it: a package, a space, a script, a versioning group, an
  initials entry, a parser type. Matching stays case-insensitive
  everywhere, because it moved to the lookups in the commit before this
  one, and two keys of one object that fold together are refused at load
  with the keys named, where the survivor used to be whichever the map
  iteration favoured.

  lowerTree becomes normalizeTree and stops lowercasing; decodeObject
  folds the key to find its setter, so `logLevel` and `loglevel` both
  load, and refuses fold-duplicates before it decodes anything. objMap,
  scriptMap and strMap refuse them per entry map; `custom` does not,
  because dispat looks nothing up in there. The flag overlay replaces the
  file's spelling of a bound key rather than sitting beside it, which
  would otherwise be a collision over a flag the operator passed
  correctly.

  The whole env restoration pass goes with it. It existed to undo the
  lowercasing for the one map that could not survive it — PATH and Path
  are two variables — by reading the parsed tree back at four levels. The
  decode does not rename anything, so there is nothing to undo:
  envRestorer, envAt, restoreEnvCase, envFromTree and
  restoreSpaceFileEnvCase are gone, and openFolderConfig no longer hands
  its callers a second view of the file. env's own fold-duplicate refusal
  stays, because an env layer is merged with the layers around it.

  Visible changes, intended: DISPAT_SPACE, DISPAT_PACKAGE and the webhook
  payloads report the names the config actually spells, and the tests that
  pinned the lowercased forms are inverted here.

### Authors

- yohimik
- Claude Fable 5
