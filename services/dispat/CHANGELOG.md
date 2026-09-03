# Changelog

## services/dispat/v1.7.2 (2026-09-03)

### Fixes

- the release announces itself and ships its experiments ([e1ac8c5](https://github.com/yohimik/dispat/commit/e1ac8c514e68679a1ad9d881386fcaa443e8e054)) (by yohimik)
  The release experiments run inside the release, as the docs package's
  beforeBuild hook against the image the run has just published, and the
  site's new release experiments page is built from what they recorded;
  the records land in coverage/experiments and never under the harness.
  tools/testreport reads the cells into the report and renders the job
  summary, replacing summary.py. Every finding of the harness review is
  fixed: the masked exit codes, the vacuous gating assert, the observer
  that fetched into the clone under test, the shim's marker and log, the
  proxy's deny rule and chunked uploads, the pinned fixture dates.

  The release announces itself on Instagram and LinkedIn with crier, from
  the announce folder at the root: a paginated card, the anthem clip, the
  stories and the LinkedIn reel with an album fallback, with a ping job
  ahead of the release and a replay workflow beside it.

  scripts/install-tools.sh pins crier and the TinyGo fork in one place;
  the tiny toolchain stage, the spike and the darwin script install
  through it, and a shellcheck gate sweeps every script.

### Authors

- yohimik


## services/dispat/v1.7.1 (2026-09-02)

### Fixes

- the tiny binaries build on the fork at 0.43.0-net.1 ([cf86355](https://github.com/yohimik/dispat/commit/cf86355d56962502c266d531d78dbe1c6004ec66)) (by yohimik, Claude Fable 5.1)
  The two dispat-tiny-linux binaries the release attaches move to the
  TinyGo fork's 0.43.0-net.1, net.4's content rebased on upstream 0.42.0,
  with the checksums the release Dockerfile verifies the toolchain
  against. The spike's base image follows upstream to 0.42.0 and its
  verdict is re-read there: a host netdev exists now and the tcp, http
  and dns layers pass, but crypto/tls is still the offload stub that puts
  plaintext on the wire, so the answer stays no and the fork stays the
  only toolchain that speaks TLS. The unit-test stage runs one package at
  a time under a bound, and internals/tinygo.md records all of it.

  The README's projects section gains crier, a single-package repository
  released through an eighteen-candidate rc train and one graduation, and
  the landing page reads it from there.

### Authors

- yohimik
- Claude Fable 5.1


## services/dispat/v1.7.0 (2026-09-02)

### Features

- install from a private github repository ([d198be3](https://github.com/yohimik/dispat/commit/d198be39909b61cb183ecdc74bd5057c6614b5b1)) (by yohimik, Claude Fable 5)
  A token now authenticates the whole install path rather than only the
  release listing: dispat install and dispat self-update fetch a private
  repository's assets from the asset API endpoint, install.sh, install.ps1
  and the GitHub action do the same, and a release carrying several files
  installs the one named after the repository without --asset.

- install the conventional asset ([18460f9](https://github.com/yohimik/dispat/commit/18460f9e575d2d5ef61bbee34621fb29fde02470)) (by yohimik, Claude Fable 5)
  A release carrying more than one file was refused unless --asset named
  one, which made the flag mandatory for almost every real repository,
  dispat's own included: its releases carry six binaries and a checksum
  file.

  Without --asset dispat now looks for the name most projects publish
  under, the repository's own name and the platform, with the extension
  selfupdate.AssetName appends on Windows. The name is matched exactly and
  never as a glob, so a bare invocation installs what the release decided
  rather than whichever near-miss sorted first, and a release that follows
  no convention is refused as before, with the name that was tried added
  to the listing so the reader knows what to answer.

  The single-asset shortcut and every explicit --asset path are unchanged.

- install scripts reach a private repository ([036d897](https://github.com/yohimik/dispat/commit/036d89711c420958068bd5b41749e347bc1cb053)) (by yohimik, Claude Fable 5)
  The bootstrap scripts authenticated the releases API and then fetched
  the binary from the public download URL with no headers, which for a
  private repository is a sign-in page served under a 200: the install
  failed on its checksum with nothing saying why.

  Both scripts now read the asset's own REST endpoint out of the release
  and, when a token was given, download from there with the octet-stream
  Accept and the bearer credential. The credential must not reach the
  object storage that endpoint redirects to, and each downloader is
  handled on its terms: curl drops Authorization across a change of host
  by itself, wget is stopped at the redirect and the location refetched
  bare, and PowerShell, which forwards the header on 5.1, is stopped the
  same way. busybox wget can do neither, so a token sent through it is
  refused rather than leaked. Without a token nothing changes.

  install.sh is now executed against a fake API, once per downloader, so
  the endpoint choice and the credential's boundary are proven rather than
  read; install.ps1 has no interpreter in the test image and is held to
  the same shape by a textual cross-check.

- download release assets with the listing's token ([8368390](https://github.com/yohimik/dispat/commit/836839032048b5056b00ab8bfec6f06f31622a40)) (by yohimik, Claude Fable 5)
  A repository the listing needed a token for serves its assets only
  through the asset API endpoint — the public download URL answers with a
  sign-in page. When the source carries a token, install and self-update
  now fetch the asset from its API endpoint with the same credential;
  without one, nothing changes.

### Fixes

- finish a conflicted release ([5b8da48](https://github.com/yohimik/dispat/commit/5b8da4826aa2eb6c4a24d89e4bd328b9b24751f3)) (by yohimik, Claude Fable 5)
  A recovery whose merge conflicted aborted and failed the run, which
  leaves a release that has already published with its commit and tags
  nowhere but the local clone. It completes instead.

  This release's side wins every conflicting file, because that is the
  tree the tag names and taking the other side would publish content the
  release never saw; everything the arriving commits changed that did not
  conflict is in the merge as it is on the clean path. Their side is
  pushed to a branch of its own, release-conflicts/ followed by what the
  leg released and a UTC timestamp, plain and never forced, so the work is
  kept rather than dropped. Both records name the conflicting files and
  that branch: the GitHub body through a note block, the changelog through
  the merge commit, since the release commit is tagged and must not be
  amended. W243 says the same thing in the log, and the run exits 0.

  The tag invariant is untouched, and the republish guard and the bounded
  retry cover this path's pushes too. E224 is now only for the recovery
  machinery failing: the quarantine branch refused, the settled merge
  uncommittable, or a merge that stopped for something other than content.

  The e2e walk gains the two cycles this is about, and the key-features
  walk keeps the private install; both are what the release build runs
  against the bytes it exports.

- keep a released tag from moving ([96cdb2b](https://github.com/yohimik/dispat/commit/96cdb2b720309d6ca8173252e0fa5501e1b87707)) (by yohimik, Claude Fable 5)
  Four ways the mid-release recovery was wrong.

  It re-pushed with commit.force, which defaults on, so a checkout stale
  enough to have re-planned an already published version would force-move
  that published tag: the push it recovers from never reached a tag ref,
  so nothing stood between the two. The remote's tags are now read first
  and the run stops, naming the tag. Aliases stay movable, because moving
  them is what every release does.

  It fired on "[rejected]" alone, and the simultaneous-push race prints
  "[remote rejected]", so the phrase it was meant to recognise was
  unreachable. The gate now matches both, and every git invocation asks
  for the C locale so a translated checkout cannot defeat the match. The
  sentinel also stopped leading the message: git's own words are what a
  reader of a failed release needs first.

  The merge was refused outright by a repository configured merge.ff=only,
  and the abort that followed failed too. It is now made with --no-ff,
  which also pins the first-parent shape the recovery documents.

  It gave up if a commit landed between its own pull and its own push,
  which is the very surprise it exists to absorb. It now goes round up to
  three times, capturing the release commit once so the tag it names never
  moves whichever round lands.

- fall back to the public URL, and read wget's answer ([f5b4760](https://github.com/yohimik/dispat/commit/f5b4760ae4211a46863d41735a366e3ba7d8bce7)) (by yohimik, Claude Fable 5)
  Three ways the authenticated download was wrong.

  An asset endpoint that refuses ended the install. A token that reads a
  repository's listing and not its assets is a real shape, and before the
  endpoint existed that install simply worked, so all three downloaders
  now try the public URL once more with no credential; the size and the
  digest still decide what lands, and when both addresses fail the refusal
  names the status the endpoint gave.

  install.sh threw away wget's exit status, so a refusal that wrote no
  file read as success. The status is kept and decides, except when a
  Location came back, which is the one answer an exit code cannot
  distinguish from a 404. The busybox probe is now the option's own exit
  code rather than a search of help text nobody promised.

  Both awk walks started at the top of the release, so a release titled
  after its own asset made the url walk print the author's account URL.
  They start at the assets array instead.

  install.ps1 caught an exception Windows PowerShell 5.1 never raises: it
  returns the refused redirect rather than throwing. The response is now
  asked for and inspected, with the 7.x exception path kept beside it.

- recognise every alias tag, whoever wrote it ([9248d6d](https://github.com/yohimik/dispat/commit/9248d6d54b42537116ee73a0da18f794fbde009e)) (by yohimik, Claude Fable 5)
  Two ways a moving alias still poisoned a baseline.

  The filter only knew the listing package's own aliases, but an alias
  belongs to whoever writes it and lands in whichever listing its shape
  matches: one package's "v1" sits in another's "v{version}" listing
  looking exactly like a release nobody can parse, and that package's
  baseline collapsed to its initials from the first alias onwards. The
  filter is now built from every package's formats, compiled once.

  The matcher assumed the version class was digits and dots regardless of
  the format, so an alias spelling a plain {version} did not recognise its
  own prerelease renders and poisoned its own package. It now reads the
  class off the format as the release-tag matcher does, walks the reduced
  shapes a prerelease-spelling format renders, and requires the text it
  captured for {version} to be a version: without that, anything the class
  allows would pass and a genuinely malformed tag would go out with it.

- record the release commit ([d3299f9](https://github.com/yohimik/dispat/commit/d3299f9673ba568a68fcf7f857507a43a46a70b1)) (by yohimik, Claude Fable 5)
  The GitHub release is created after the push, and after a mid-release
  recovery HEAD is the merge by then, so the "commit" line in its body and
  its target_commitish named the merge rather than the release the record
  is about.

  The recovery already reads the release commit before merging, since the
  merge message names it. It now hands that back, and the finalize phase
  prefers it over HEAD when stamping the releasers. A run with no recovery
  sets nothing and reads HEAD exactly as before.

- merge what landed during a release instead of rebasing ([5b74cd5](https://github.com/yohimik/dispat/commit/5b74cd5d08f60b0ad8a0bf7dda13a8a3d8aaa00e)) (by yohimik, Claude Fable 5)
  The recovery from a push somebody pushed under replayed the release
  commit on top of what arrived, which rewrote it: the tags had to be
  moved onto the replacement, a release pinned to a commit its own scripts
  made could not be recovered at all, and the commits that arrived ended
  up inside the window the release closed, so nothing ever released them.

  It now merges instead. Nothing the run made is rewritten, so the release
  commit keeps its identity and its tags keep naming it: the tagged tree
  still carries the changelog entries and version rewrites the release
  recorded, which is what anything resolving the tag reads. Only the
  branch tip changes, into a merge whose first parent is the release
  commit and whose second is what arrived.

  That leaves the arriving commits outside the tag's ancestry, which is
  where they belong: the next run plans them and releases them with
  records of their own. The merge commit is a chore(release), so the scope
  nonPackageScopes exempts keeps it from naming a package, and it carries
  the release commit and its tags in its body so the join can be audited.

  The merge is made from the branch rather than onto the fetched tip.
  Either parent order reads the same to the planner, which finds an
  exempted scope on the merge either way, but this one is a single command
  that a single command undoes: a conflict aborts back to exactly the tree
  the run had, rather than mid-merge or on a detached HEAD.

- read past a package's own alias ([4edbab5](https://github.com/yohimik/dispat/commit/4edbab5dca43d159b45fe05d297c1c7cb55c0804)) (by yohimik, Claude Fable 5)
  A single-package repository releasing as "v1.4.2" could not declare the
  "v1" a GitHub composite is consumed through: the load-time check refused
  any alias whose name matched a package's tagFormat, and "v1" matches
  "v{version}" on the prefix alone. The refusal existed because the
  baseline reader would take the alias for the newest release tag and read
  no version out of it, leaving the package looking unreleased.

  Both halves now ask what a name can be read back as rather than what it
  looks like. The check refuses an alias only when it parses as a release
  tag, so "v1.4.2" beside "v{version}" stays refused and "v1" becomes
  legal. The tag listing drops a name that carries no version when one of
  the package's own alias formats could have written it, which is precise
  enough to leave a mistyped release tag like "v1.0.0.0" in place, where
  the initials fallback still measures the window from it.

  Recognising an alias is a matcher rather than a prefix test, so {major},
  {minor} and {patch} capture one number and stop at a separator. Both
  readers of a baseline go through the same filter: the planner and the
  compute command's manifest baselines.

- recover a release push somebody pushed under ([91551ef](https://github.com/yohimik/dispat/commit/91551efe4b27f3d8381b57e8a55ec1d2fef8350c)) (by yohimik, Claude Fable 5)
  The behind-remote guard closes before the plan is computed, so a commit
  pushed while the run is working reaches the finalize push as a
  rejection. The packages have already published by then, and the run
  ended with the release commit and its tags nowhere but the local clone,
  which nothing goes back for.

  The push now tells a branch that moved apart from a remote nobody could
  reach, and the first is recovered: the branch is pulled, the release
  commit replayed on top of what landed, the tags moved onto the replayed
  commit and the push retried. The tags therefore only ever reach the
  remote on the commit that reached it too. W242 reports the recovery,
  because the release went out on a tree that is not the one it was
  planned against.

  A replay that conflicts, and a release whose tag is pinned to a commit
  its own scripts made, are both refused with an error naming that commits
  landed during the release: the rebase is undone, no tag is pushed, and
  the lock is given back as on every other way out.

  The commit.verify guard scenario asserted the old failure and now
  asserts the recovery: what that guard protects is the plan, not the
  push.

- say what the release token now unlocks ([a754978](https://github.com/yohimik/dispat/commit/a7549788fb95f4c76856fe3107a0b9ac8aee1fc9)) (by yohimik, Claude Fable 5)
  The token stopped being only a rate-limit lever when the asset download
  started using it, but the godoc on Source.Token, the comment behind the
  GITHUB_TOKEN fallback and the download's own commentary all still said
  so. They now say that a public repository needs no token and a private
  one needs it for both the listing and the asset.

  The download also had no record of the choice it makes. It now logs the
  asset, the address it went to and whether the request was authenticated,
  at debug level and never the credential itself, so a failed private
  install can be told apart from a public one that went the usual way.

- enumerate release pages on PowerShell 7.6 ([c5ebc04](https://github.com/yohimik/dispat/commit/c5ebc0436209d048797163609a642a31273183cd)) (by yohimik, Claude Fable 5)
  Invoke-RestMethod stopped enumerating JSON arrays, so a page arrived as
  one Object[] and the tag filter became member enumeration over every
  tag — where a shorter tag from another package made Substring throw.
  Seen as a hard failure in every Windows 'Set up dispat' action step.

### Dependencies

- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.7.0): 1.6.0 -> 1.7.0

### Authors

- yohimik
- Claude Fable 5


## services/dispat/v1.6.0 (2026-08-31)

### Features

- dispat for runs a script once per item ([2aa6053](https://github.com/yohimik/dispat/commit/2aa60534c97080afd303c944339bbface82123cd)) (by yohimik, Claude Fable 5)
  The third shell helper, beside if and exec. A POSIX `for x in ...; do
  ...; done` copied into a configured script is the one construct that
  breaks the moment `shell` names something else, and a loop is what a
  script reaches for the instant it has more than one thing to do.

  The list comes from exactly one source: positional items, which need no
  configuration at all; -p, -s or -g, which iterate over the packages the
  terms name, over the spaces themselves, or over the versioning groups
  themselves; or --changed, --unchanged and a bare --since, which iterate
  over the window every sweeping command covers and over its complement.
  Under a window source the same three selection flags stop being the
  source and become the narrowing they are everywhere else, exactly as
  they compose for `dispat if --changed`.

  Every iteration exports DISPAT_ITEM, DISPAT_INDEX and DISPAT_TOTAL,
  appended last so nothing an item or an enclosing run carries can shadow
  them, plus the release environment's own names for what the item is:
  DISPAT_PACKAGE, DISPAT_SPACE, DISPAT_DIR and DISPAT_GROUP. A script
  therefore moves between a release stage and a loop unchanged.

  Like `dispat if`, the command conditionally requires configuration: a
  literal list reads nothing and starts no update check, and every source
  that asks about the monorepo defers to the configured phase, where the
  loop body also picks up the configured shell -- which is the whole point
  of the command.

- the release environment names the versioning group ([a06b54f](https://github.com/yohimik/dispat/commit/a06b54fcbb338428c13a913a21081e5f7cbc44c6)) (by yohimik, Claude Fable 5)
  DISPAT_GROUP is the package's third address, beside its name and its
  space: a script that has to know which packages move together with this
  release could not derive it from the other two, since a group may span
  spaces and a space may version independently. It is rendered from the
  package's own group -- a declared versionGroups entry, or the space's
  name where a space versions as one -- and left unset rather than empty
  for an independently versioned package, the same unset-not-empty
  convention DISPAT_COUNTER keeps.

- the step environment carries each update's tag ([ed151aa](https://github.com/yohimik/dispat/commit/ed151aa3812eaca304f224f93146864ac82573a3)) (by yohimik, Claude Fable 5)
  A nested step command that aligns its updates to the run rebuilt them
  from the environment without their tags, and the renderer rightly
  declines an auto dependency link it cannot spell, so an aligned record
  rendered plain lines where the run's own record linked them. The
  listing now writes DISPAT_UPDATED_<KEY>_TAG beside the name and the
  versions, the step reader carries it back, and absence stays legal: a
  parent run predating the variable leaves the tag empty and the decline
  applies as before. Tags alone are never drift, because a tag mismatch
  is not a movement mismatch and either side of the alignment rendered
  its tags through the same formats.

### Fixes

- the loop speaks at the level if does ([a710926](https://github.com/yohimik/dispat/commit/a710926db91b69bc2159fade9867d73435576fdf)) (by yohimik, Claude Fable 5)
  The shell helpers are glue, and if says everything at debug: a chosen
  branch, an empty result, nothing at info. for's summary and its empty
  list said the same kinds of things one level louder, so a quiet pipeline
  gained lines its scripts did not write. Both drop to debug; the
  --require-items refusal stays an error, because a refusal is not
  narration.

- every source package of a unit reaches DueTo ([c7bfcb5](https://github.com/yohimik/dispat/commit/c7bfcb57978e41752f703c187a38b49ef3c5cf44)) (by yohimik, Claude Fable 5)
  A unit written over several packages propagated its bump correctly but
  credited only the package the traversal happened to arrive from: the
  walk visits a target once, and the first source out of the queue was
  the one recorded. A consumer of all of them was told it releases
  because of one, and the miscredit was not cosmetic. A catch-up record
  reaches a provider that is not releasing only through the attribution,
  so the dependencies section listed one movement and silently dropped
  the rest, and a release explained by a releasing source was labelled a
  catch-up whenever the credited source happened to be the one already
  shipped. Section 9.2 attributes the whole source set, prov[d] |=
  sources, and the plan now records one contribution per source package.

### Dependencies

- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.6.0): 1.5.0 -> 1.6.0

### Authors

- yohimik
- Claude Fable 5


## services/dispat/v1.5.0 (2026-08-31)

### Features

- sections, links and refs on release records ([b14b1f3](https://github.com/yohimik/dispat/commit/b14b1f3ae83f1963a76c80eea8d8de9c4ddb3992)) (by yohimik, Claude Fable 5)
  An entry could say four things it had no way to say. It can now.

  `sections` states the whole render order of an entry, the built-ins and
  sections of its own together, and a custom section claims commit types out of
  the bump-keyed grouping. A section may declare the bump its types carry, which
  merges into the parser's one type table, so declaring the section that renders
  `add` is what makes an `add` commit release at all. Breaking always wins the
  grouping: letting `add(x)!:` sit under "Added" would hide the one thing a
  reader scans an entry for. A built-in the list omits is appended after the
  listed ones rather than dropped, because a section removed in silence takes
  released work out of the record with it. The bump belongs to the root file,
  since the parser is built once for the repository, and a folder's own config
  file is refused with the reason rather than left with a section nothing reaches.

  `dependencyLink` links a dependency line to the release the provider moved to,
  and `commitRefs` names the commit behind each entry line. Both take a URL
  template or `auto`, which derives github.com's own URLs from the package's
  owner and repo, and both fall back to the plain text rather than to a link that
  leads nowhere: a record is published and permanent, and there is no later run
  in which a broken link comes out right. The provider's tag is rendered by the
  plan through the provider's own tagFormat, and a unit the planner has no sha
  for is left unreferenced and reported once per release under W240.

  `noChangesText` replaces the sentence an entry with nothing to group carries.
  It falls back to the built-in sentences when it expands to nothing, because an
  entry that renders empty reads as a broken write.

  The renderer moves into sections.go with one change of shape that is a fix
  rather than an option: a commit body is indented two spaces under its bullet,
  so the paragraphs after the first stay inside the list item instead of ending
  it, and every section now closes on exactly one newline whether or not its last
  line carried a body.

- unknown config keys hint at self-update ([947c3e5](https://github.com/yohimik/dispat/commit/947c3e56af8d37274bb8231ee8cc52b84cb46229)) (by yohimik, Claude Fable 5)
  A key the loader has no field for is usually a typo, and pkg/config already
  says so. The cause it cannot know about is a configuration written for a newer
  dispat than the one reading it: the key is real, in a schema this binary
  predates, and the file is right while the binary is behind. An operator who
  reads only "unknown key" spends the next minutes hunting a spelling mistake
  that is not there.

  Every place dispat surfaces a config load failure, the root file and a folder's
  own package or space file alike, now appends the other explanation and names
  the command that answers it, `dispat self-update --check`. The hint is dispat's
  rather than the library's, because pkg/config knows nothing about dispat's
  releases or how one updates, and it wraps rather than replaces, so errors.Is
  and errors.As still reach what the loader reported.

### Fixes

- record rendering hardened after review ([24cf9b8](https://github.com/yohimik/dispat/commit/24cf9b83f7c7146227c013c705877fe737d118e4)) (by yohimik, Claude Fable 5)
  A review pass over the new record features closed what it found. An entry
  whose file title renders empty for the release keeps the file's own head
  instead of writing above it, and a preamble containing a fenced example is
  split after the fence rather than inside it. An auto link declines when the
  step environment aligned the updates without their tags, when the resolved
  API URL points outside github.com, and when the repository pair is only half
  configured; the half pair is no longer completed from the environment, and
  the completion itself moved from the renderer to the recorders, so rendering
  no longer reads the process environment at all. A record link inherited from
  a broader layer can be declined with the value off. A no-changes text that
  expands to nothing falls back to the built-in line and says so as W241, and
  one carrying a thematic break on any line is refused at load. The auto base
  is derived once per entry rather than once per line.

- the dependencies section stays a tight list ([de93ba8](https://github.com/yohimik/dispat/commit/de93ba8a80c9543bb55475119e42e681232bc6ab)) (by yohimik, Claude Fable 5)
  The loose joining the sections gained for their bullet-and-body items had
  reached the dependencies list, and a multi-provider entry rendered with a
  blank line between movements. The section is a table: its lines never carry
  bodies, and every changelog written so far renders them as one block, so the
  tight joining is restored there alone. The command pages state the exception.

- entry spacing and adopted changelog preambles ([7e71494](https://github.com/yohimik/dispat/commit/7e714946cc4c09b0d3d899610fe54b7260b823e6)) (by yohimik, Claude Fable 5)
  Two things the changelog writer got wrong, both of them about bytes it did not
  write.

  The seam between one entry and the next was whatever the entry above it
  happened to end with, so a release whose last section was a dependencies list
  spaced differently from one that ended on a bullet with a body, and a file
  recorded the shape of each release rather than one rule. The writer now closes
  an entry on exactly one newline and writes exactly `changelog.entrySpacing`
  blank lines below it, two by default. Only the seam is written: the entries
  underneath keep the spacing and the line endings they were published with.

  Adoption was the sharper one. A file that does not open with the title dispat
  renders was prepended to, which published a second H1 over the file's own and
  pushed YAML front matter off the head of the file, where it stops being front
  matter at all. Everything above the first entry heading is now the file's
  preamble: it stays at the top, the new entry goes in below it, and dispat's own
  title is never written into a file that already has one. A byte-order mark is
  carried through at the very head and cut before the title match and the
  entry-exists check, and a title terminated with CRLF is matched line by line,
  because a title the strip fails to see is a title written twice.

### Dependencies

- [config](https://github.com/yohimik/dispat/releases/tag/pkg/config/v1.0.0): 0.0.0 -> 1.0.0
- [models](https://github.com/yohimik/dispat/releases/tag/pkg/models/v1.5.0): 1.4.0 -> 1.5.0

### Authors

- yohimik
- Claude Fable 5


## services/dispat/v1.4.0 (2026-08-30)

### Features

- minified linux binaries ride the release
A release now carries dispat-tiny-linux-amd64 and
dispat-tiny-linux-arm64 beside the six it always has: the same source
and the same version stamp, built by the TinyGo fork at 0.42.0-net.4,
at roughly 60% of the bytes. They are additive downloads under names
of their own, so dispat-<os>-<arch>, which self-update and install.sh
resolve, is untouched.

The toolchain arrives as its release tarball by URL and against the
digest that release published, not through dispat install: a build of
dispat that needs a working dispat to start is a bootstrap cycle. The
checksum is the part install would have done, kept. Debian rather
than alpine, because the fork ships a glibc-linked LLVM and musl does
not run it; what comes out is static either way.

A TinyGo binary carries no Go build info, so the tiny pair is proven
by running it rather than by reading it back, which the smoke loop
now does for all four linux binaries. The spike stays the deep gate,
and internals/tinygo.md carries what those binaries can do.
- the loaded configuration says what it holds
The post-load debug line said which file was read and which folder
became the root, which answers "did it read the file I meant" and
nothing after it. A configuration that read as almost nothing — a $ref
resolving to an empty fragment, a spaces object under a key nobody
meant — still looks like a run that simply found no work.

So the line now counts what the loader made of the file: the package
entries it names, the scripts it binds and the webhooks it notifies,
across every level of the root file that may declare one. In-folder
files are deliberately not counted; they are read later, and a number
that grew afterwards would describe a configuration nobody had yet.
- first-party config decoder
The config language is a table now: one entry per key a file may write,
saying what writing it does. A key with no entry is a key the model has
no field for, so the unknown-key refusal every typo lands in is
structural rather than a setting somebody remembered to turn on.

What this replaces is a reflected decoder told its exceptions through
hooks that fire on a Go type and cannot see the key that produced it.
The hazard was never hypothetical: the conversion lifting a scalar into
a list splits it on commas, which is right for a list of script names
and wrong for a shell command, and the two were kept apart only by the
order the hooks were composed in. They are different setters here, so
the order that used to matter cannot exist.

Nothing calls it yet. fields_test.go reads the models' own json tags and
refuses any disagreement with the tables in either direction, and
decode_parity_test.go runs a corpus through both decoders and fails on
any difference the migration did not declare.
- the command that installs a tool is called install
The word says what the command leaves behind rather than how it gets
there: `dispat install <repo>` puts a verified release asset on PATH,
and download was the mechanism, not the outcome. The flags, the
behavior and the machinery are unchanged; the command word, the
package (internal/install), the error prefixes and the report texts
follow the new name, and every doc page moves with it.

`install` permanently shadows a run script of the same name, as every
command word does; a script called install stays reachable as `dispat
run install` and from flow sequences, which the example configs use it
in.
- download installs a tool from any github release
dispat download <repo> is self-update pointed at somebody else's
repository: the same listing walk, the same streamed download checked
against the published size and checksum, the same two renames that keep
what they replace. It needs no config file and no git repository.

The repository is named however it is at hand, and a host that is not
github.com derives a GitHub Enterprise endpoint. --asset says which of
the release's files is the binary, as a name or a glob, with {os},
{arch}, {version}, {tag} and {name} expanded; nothing is guessed, since
the wrong guess is installed globally and run. --bin-dir and --as say
where it goes and what it is called, defaulting to the ladder install.sh
climbs. --pipe hands the verified file to a command in that folder
instead, which is how an archive is unpacked and a release's own install
script is run.

The destination is hashed against the release's digest, so the command
is idempotent: --check gates on it and --force installs over it.
--rollback restores what the last download replaced. GITHUB_TOKEN is
sent to github.com alone, because the endpoint comes from an argument.

### Fixes

- a record line is read after it is decoded
A record line written as an object went through `return line,
decodeObject(item, at, entryLineFields(&line))`, which leaves the order
of copying line and running the call to the compiler. gc runs the call
first and returns the filled line; TinyGo copies line first and returns
it empty, so every object-form footer, header and fileTitle decoded to
nothing and the load failed with "line is required". Found by the
0.42.0-net.2 validation run; no assertion can catch it under gc, so the
sequencing comment is the guard.

### Dependencies

- models: 1.3.0 -> 1.4.0

## services/dispat/v1.3.1 (2026-08-28)

### Fixes

- catch-up records span the provider's movement
A catch-up picks up a provider that published in an earlier run, so by the
time its records are written the provider's own before-and-after have
collapsed onto the published version — and the changelog entry, the GitHub
release body and the DISPAT_UPDATED_* variables all said "1.3.0 -> 1.3.0",
a movement line with no movement, which is what the docs leg of the 1.3.0
release shipped. From is now what the package's previous release shipped
against, reconstructed off the provider's tags at the package's own
baseline, the same way a graduation spans its train; the step commands
inherit the span through the plan they recompute.

A ride catching up documents the movement it rode for by the same
reconstruction: its provider is not releasing and nothing propagated, so
the record loops found nothing at all, and the ride's entry stayed silent
about the one thing it existed to ship — where the same ride in a
single-run release names the provider's movement. An own-cause release's
manifest-only pickup stays out of the record, as it always has.

## services/dispat/v1.3.0 (2026-08-28)

### Features

- changelogs and github releases authors


### Dependencies

- models: 1.2.0 -> 1.3.0

## services/dispat/v1.2.0 (2026-08-27)

### Features

- external webhooks


### Dependencies

- models: 1.1.0 -> 1.2.0

## services/dispat/v1.1.1 (2026-08-26)

### Fixes

- window-only run selections without the script no-op


## services/dispat/v1.1.0 (2026-08-20)

### Features

- self-update prints changelog


### Dependencies

- models: 1.0.0 -> 1.1.0

## services/dispat/v1.0.2 (2026-08-19)

### Dependencies

- manifest: 1.1.0 -> 1.1.1
- scanner: 1.1.0 -> 1.1.1
- writer: 1.1.0 -> 1.1.1

## services/dispat/v1.0.1 (2026-08-19)

### Fixes

- manifest libraries updated providing unity, unreal, godot, o3de and defold manifests supported


### Dependencies

- manifest: 1.0.0 -> 1.1.0
- scanner: 1.0.0 -> 1.1.0
- writer: 1.0.0 -> 1.1.0

## services/dispat/v1.0.0 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces

- rename autosubstute to autoreplacer

- restore the 1.0 rc train

- replace the channel sigil with percent


### Features

- the release engine is ready for the stable line

- finalize the workspace for 1.0.0

- the installers explain PATH permanence and shadowing

- debug shows git mutations, trace shows starting scripts

- the run start and the lock answer at info

- warn a github step running before the run's tag

- step commands align to the run's environment

- release polyglot monorepos from conventional commits

- report the lock holder and age in the refusal

- trace scope, propagation and group derivation

- surface .dispatexclude exclusions at debug

- reconcile missing assets on an existing github release

- retry transient github lookups with backoff

- wire multi path spaces downstream

- accept a list of space paths

- report and guard none packages

- exclude none packages from the release plan

- reject releasable deps on none packages

- add versioning none mode

- expand the changed window before narrowing

- wire if changed and file conditions

- add changed selection lookup

- add resolved conditions

- exit 3 when --require-release finds nothing to release

- preview the changelog or github body

- choose the channels lines and records reach

- let a $ref name several files

- suppress the reverted changelog entries

- mark and render the corrected entries

- apply the Edits and Deletes corrections

- gate local links and dependency ranges from the scanner

- one location grammar for exec's subject, script source and folder

- load .env files

- write through a $ref in compute

- resolve $ref in config files

- trace what each script actually ran

- array of scripts, require release

- forward arguments after -- to run and exec scripts

- trace and debug logging for git, config and the plan

- add the autosubstitute command

- autowriter derives edits from the workspace

- change scope ignore rules

- root and space level flags

- space dependencies

- unsafeDisableLock config field

- lock the release command

- alias tags

- force tag writes and pushes

- consumer-keyed dependencies

- version component env vars

- if and exec shell helpers

- commit --tag-name

- allowBranch and behind-remote release guards

- self-update from the latest stable release

- autoreplace rewrites manifests across the selection

- select packages by versioning group

- release and status narrow to the package selection

- a package src path that narrows change detection

- per-command help, a platform in the version, and quiet parser diagnostics

- a github step command, and releases that skip themselves on a re-run

- prerelease opt-out for changelog and github records

- reconcile Docker image tags at the version stage

- fixedMajor and fixedMajorMinor versioning modes

- space packages entries and the space folder config layer

- space file model and .dispatignore over config names

- reconcile with replace rules

- select the autoversion strategy

- declare manifest names per package

- add the replacer command

- select packages with --package and --space

- expose the scanner and writer as commands

- resolve scripts per package across three levels

- changelog, commit and autoversion commands

- build release binaries in dispat flow

- run consumers

- package add

- per-package overrides, version groups, dispatignore

- preview all packages

- shared manifest module, package readmes

- compute auto version

- run since

- export

- preview, init, test, config


### Fixes

- exercise propagation out of the group driver

- exercise a group ride release

- exercise the release pipeline across every package

- the skip cascade reads the fresh changeset, not the train

- a graduation documents the train's provider movement

- config edits prepare every file before writing any

- self-update no longer claims an empty install path

- a record entry is never empty

- a catch-up on a train is still a catch-up

- status counts the fresh changeset, not the train

- a ride with train history still says no changes

- nested dependencies update again x3

- nested dependencies update againx2

- the reason names what forces the release

- a spent blast and a distant origin leave the records

- nested dependencies update again

- nested dependencies update

- dependencies update

- a wired record states the run's provider movements

- the module's go directive matches the workspace

- the installers walk the release listing past page one

- carry the license in every module

- the release commit names only what it records

- an explicit DISPAT_UPDATE_CHECK=1 waits for the answer

- warn when a commit.include path is missing

- refuse ambiguous initials under case-colliding names

- refuse an unselectable if --changed --consumers

- published log names the deferred tag

- pre-config errors respect the log flags

- scale the github upload timeout to the asset

- write changelogs through an atomic replace

- report a correction that reached nothing

- count references followed, not keys walked, when bounding an edit

- name the sparse member that decides a group's major

- run the login in the space folder

- keep alias tags out of the release commit subject

- sync manifests and changelogs for every updated provider

- log every W diagnostic at warn level

- 1.0.0 release blockers

- never abort a run after a release is out

- resolve the known groups once per invocation

- scope the group pin and channel conflicts to a moving group

- keep W222 for rules that actually reached a file

- step over an unreadable folder in a replace rule's walk

- run syncLock without a reconciling strategy

- load ancestry dag once, parallel tag reads

- scheduler fifo, script cancellation, launch determinism

- autoversion and compute correctness

- round 2 blockers

- ccme and models API freeze

- graceful shutdown, ancestry cache, scheduler guard

- 1.0.0 blockers

- badges

- git check


### Dependencies

- ccme: 1.0.0-rc.10 -> 1.0.0
- manifest: 1.0.0-rc.10 -> 1.0.0
- models: 1.0.0-rc.19 -> 1.0.0
- scanner: 1.0.0-rc.10 -> 1.0.0
- writer: 1.0.0-rc.10 -> 1.0.0

## services/dispat/v1.0.0-rc.19 (2026-08-16)

### Features

- the release engine is ready for the stable line


### Dependencies

- ccme: 1.0.0-rc.9 -> 1.0.0-rc.10
- manifest: 1.0.0-rc.9 -> 1.0.0-rc.10
- models: 1.0.0-rc.18 -> 1.0.0-rc.19
- scanner: 1.0.0-rc.9 -> 1.0.0-rc.10
- writer: 1.0.0-rc.9 -> 1.0.0-rc.10

## services/dispat/v1.0.0-rc.18 (2026-08-16)

### Dependencies

- models: 1.0.0-rc.17 -> 1.0.0-rc.18

## services/dispat/v1.0.0-rc.17 (2026-08-16)

### Fixes

- exercise propagation out of the group driver


### Dependencies

- models: 1.0.0-rc.16 -> 1.0.0-rc.17

## services/dispat/v1.0.0-rc.16 (2026-08-16)

### Dependencies

- writer: 1.0.0-rc.8 -> 1.0.0-rc.9
- manifest: 1.0.0-rc.8 -> 1.0.0-rc.9
- models: 1.0.0-rc.15 -> 1.0.0-rc.16
- scanner: 1.0.0-rc.8 -> 1.0.0-rc.9

## services/dispat/v1.0.0-rc.15 (2026-08-16)

### Dependencies

- ccme: 1.0.0-rc.8 -> 1.0.0-rc.9
- models: 1.0.0-rc.14 -> 1.0.0-rc.15

## services/dispat/v1.0.0-rc.14 (2026-08-16)

### Dependencies

- models: 1.0.0-rc.13 -> 1.0.0-rc.14

## services/dispat/v1.0.0-rc.13 (2026-08-16)

### Fixes

- exercise a group ride release


### Dependencies

- models: 1.0.0-rc.12 -> 1.0.0-rc.13

## services/dispat/v1.0.0-rc.12 (2026-08-16)

### Fixes

- exercise the release pipeline across every package


### Dependencies

- ccme: 1.0.0-rc.6 -> 1.0.0-rc.7
- manifest: 1.0.0-rc.6 -> 1.0.0-rc.7
- models: 1.0.0-rc.11 -> 1.0.0-rc.12
- scanner: 1.0.0-rc.6 -> 1.0.0-rc.7
- writer: 1.0.0-rc.6 -> 1.0.0-rc.7

## services/dispat/v1.0.0-rc.11 (2026-08-16)

### Features

- the installers explain PATH permanence and shadowing

- debug shows git mutations, trace shows starting scripts


### Fixes

- the skip cascade reads the fresh changeset, not the train

- a graduation documents the train's provider movement

- config edits prepare every file before writing any

- self-update no longer claims an empty install path

- a record entry is never empty

- a catch-up on a train is still a catch-up

- status counts the fresh changeset, not the train

- a ride with train history still says no changes


### Dependencies

- models: 1.0.0-rc.10 -> 1.0.0-rc.11

## services/dispat/v1.0.0-rc.10 (2026-08-16)

### Fixes

- nested dependencies update again x3


### Dependencies

- models: 1.0.0-rc.9 -> 1.0.0-rc.10

## services/dispat/v1.0.0-rc.9 (2026-08-16)

### Fixes

- nested dependencies update againx2


### Dependencies

- ccme: 1.0.0-rc.5 -> 1.0.0-rc.6
- manifest: 1.0.0-rc.5 -> 1.0.0-rc.6
- models: 1.0.0-rc.8 -> 1.0.0-rc.9
- scanner: 1.0.0-rc.5 -> 1.0.0-rc.6
- writer: 1.0.0-rc.5 -> 1.0.0-rc.6

## services/dispat/v1.0.0-rc.8 (2026-08-16)

### Fixes

- the reason names what forces the release

- a spent blast and a distant origin leave the records


### Dependencies

- writer: 1.0.0-rc.4 -> 1.0.0-rc.5
- manifest: 1.0.0-rc.4 -> 1.0.0-rc.5
- models: 1.0.0-rc.7 -> 1.0.0-rc.8
- scanner: 1.0.0-rc.4 -> 1.0.0-rc.5

## services/dispat/v1.0.0-rc.7 (2026-08-16)

### Dependencies

- ccme: 1.0.0-rc.4 -> 1.0.0-rc.5
- manifest: 1.0.0-rc.3 -> 1.0.0-rc.4
- models: 1.0.0-rc.6 -> 1.0.0-rc.7
- scanner: 1.0.0-rc.3 -> 1.0.0-rc.4
- writer: 1.0.0-rc.3 -> 1.0.0-rc.4

## services/dispat/v1.0.0-rc.6 (2026-08-16)

### Fixes

- nested dependencies update again

- nested dependencies update


### Dependencies

- ccme: 1.0.0-rc.3 -> 1.0.0-rc.4
- manifest: 1.0.0-rc.2 -> 1.0.0-rc.3
- models: 1.0.0-rc.5 -> 1.0.0-rc.6
- scanner: 1.0.0-rc.2 -> 1.0.0-rc.3
- writer: 1.0.0-rc.2 -> 1.0.0-rc.3

## services/dispat/v1.0.0-rc.5 (2026-08-16)

### Fixes

- dependencies update

- a wired record states the run's provider movements


### Dependencies

- ccme: 1.0.0-rc.2 -> 1.0.0-rc.3
- manifest: 1.0.0-rc.1 -> 1.0.0-rc.2
- models: 1.0.0-rc.4 -> 1.0.0-rc.5
- scanner: 1.0.0-rc.1 -> 1.0.0-rc.2
- writer: 1.0.0-rc.1 -> 1.0.0-rc.2

## services/dispat/v1.0.0-rc.4 (2026-08-16)

### Breaking Changes

- commit to the 1.0 interfaces


### Features

- the run start and the lock answer at info

- warn a github step running before the run's tag

- step commands align to the run's environment

- release polyglot monorepos from conventional commits


### Fixes

- the module's go directive matches the workspace

- the installers walk the release listing past page one

- carry the license in every module

- the release commit names only what it records

- an explicit DISPAT_UPDATE_CHECK=1 waits for the answer


### Dependencies

- ccme: 1.0.0-rc.1 -> 1.0.0-rc.2
- manifest: 1.0.0-rc.0 -> 1.0.0-rc.1
- models: 1.0.0-rc.1 -> 1.0.0-rc.4
- scanner: 1.0.0-rc.0 -> 1.0.0-rc.1
- writer: 1.0.0-rc.0 -> 1.0.0-rc.1

## services/dispat/v1.0.0-rc.3 (2026-08-09)

### Fixes

- load ancestry dag once, parallel tag reads


## services/dispat/v1.0.0-rc.2 (2026-08-09)

### Breaking Changes

- replace the channel sigil with percent


## services/dispat/v1.0.0-rc.1 (2026-08-09)

### Features

- changelog, commit and autoversion commands


## services/dispat/v1.0.0-rc.0 (2026-08-09)

### Breaking Changes

- initial release

