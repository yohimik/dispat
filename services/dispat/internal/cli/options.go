package cli

import (
	"github.com/spf13/pflag"

	"github.com/yohimik/dispat/services/dispat/internal/app"
)

// options holds a pointer to every declared flag's value. Grouping them
// keeps Run about dispatch rather than about forty local variables, and it
// is the list the command table in usage.go names by flag name — the two
// together are the whole flag surface.
type options struct {
	// global
	root, cfgName         *string
	envFiles              *[]string
	logLevel, logFormat   *string
	quietParser           *bool
	showVersion, showHelp *bool

	// run
	onError, since *string
	consumers      *bool

	// selection, shared by every package-selecting command
	pkgFilter, spaceFilter, groupFilter *[]string

	// init
	initFormat *string

	// preview
	pvChangelog, pvGithub *bool

	// compute
	computeWrite, computeInteractive *bool

	// self-update
	suRelease                         *string
	suForce, suPrerelease, suRollback *bool

	// download, which shares --release, --prerelease, --force and --rollback
	// with self-update: the same four questions about the same kind of
	// release, asked of another repository.
	dlAsset, dlBinDir, dlName, dlPipe, dlTagPrefix *string

	// check is compute's CI gate and self-update's "would this change
	// anything": one flag, because it answers the same question for both.
	check *bool

	// commit
	commitTag, commitPush, commitNoForce                                *bool
	commitName, commitEmail, commitRemote, commitMessage, commitTagName *string
	commitInclude                                                       *[]string

	// github
	ghOwner, ghRepo, ghAPIURL, ghTokenEnv, ghTarget *string

	// changelog
	clFile, clFileTitle, clDateFormat *string

	// shared by the changelog and github step commands
	releaseName *string
	// the authors entry-format options, shared by the same two commands
	authors, authorsFormat, authorsCommits, authorsTitle *string
	authorsInclude, authorsExclude                       *[]string

	// autoversion and autowriter
	avRange, avManifests                    *string
	avMatch                                 *[]string
	avNoReplace, avWriteVersion, avSyncLock *bool
	onlyUpdated                             *bool

	// if
	ifThen, ifElif *[]string
	ifElse         *string
	ifChanged      *bool
	ifDir          *string

	// exec
	execFor, execScriptFrom, execEnv *string
	execFallback                     *bool

	// shared by the two shell helpers
	onFailure *string
	helperIn  *string

	// scanner, writer, replacer
	scanRootOnly                           *bool
	scVerifyUnlinked, scVerifyLinked       *bool
	scForbidRange, scRequireRange          *[]string
	wrSetVersion, wrSetBuild               *string
	wrSet, wrLink                          *[]string
	wrSetLocal, wrLinkLocal, wrUnlinkLocal *bool
	wrDropLinks                            *bool
	rpReplace                              *[]string
	rpFiles                                *[]string
	strict                                 *bool

	// release and status: the CI gate that turns an empty plan into a failure.
	requireRelease *bool
}

// declareFlags declares every flag on fs and records the pointers. It is
// separate from Run so that the flag surface can be built — and checked
// against the command table — without running a command.
func declareFlags(fs *pflag.FlagSet) *options {
	o := &options{}
	o.root = fs.String("root", ".", "monorepo root folder")
	o.cfgName = fs.String("config", "dispat.json",
		"config file name, relative to --root; when not set, the first of dispat.json, dispat.yaml, dispat.yml, dispat.toml that exists")
	o.envFiles = fs.StringArray("env-file", nil,
		"read environment variables from this file instead of ./.env (repeatable, later files win); variables the environment already sets are kept")
	fs.IntSlice("concurrency", nil, "override the configured concurrency: one value for both stages, or build,publish (e.g. 4,2); dispat run uses the build value")
	o.logLevel = fs.String("log-level", "", "override the configured logLevel (trace, debug, info, warn, error)")
	o.logFormat = fs.String("log-format", "", "override the configured logFormat (pretty, json)")
	o.quietParser = fs.Bool("quiet-parser", false,
		"hide the commit-message parser's diagnostics; --quiet-parser=false shows them again when parser.quiet is set")
	o.onError = fs.String("on-error", app.OnErrorSkip,
		"what a failed package does to its dependents (skip or continue)")
	o.since = fs.String("since", "",
		"select the packages the commits since the git revision address (scopes first, changed files for scopeless commits; e.g. HEAD~1, origin/main, a tag; 'all' selects every package) instead of the release window")
	o.consumers = fs.Bool("consumers", false,
		"additionally run every package that transitively depends on a selected one, so downstream consumers are re-run with the change")
	o.pkgFilter = fs.StringSliceP("package", "p", nil,
		"narrow to the named packages (repeatable and comma-separated; '*' globs, so -p '*' is every package and -p '@acme/*' a prefix); without it, the package folder the command was invoked from")
	o.spaceFilter = fs.StringSliceP("space", "s", nil,
		"narrow to every package of the named spaces (repeatable, comma-separated, '*' globs); a standalone package belongs to no space, so --package is the only way to name one")
	o.groupFilter = fs.StringSliceP("group", "g", nil,
		"narrow to every package of the named versioning groups (repeatable, comma-separated, '*' globs); a group is a versionGroups entry or a space that versions as one, so it may cross spaces")
	o.initFormat = fs.String("format", "json",
		"config file format (json, yaml or toml)")
	o.pvChangelog = fs.Bool("changelog", false,
		"preview the changelog entry body (the default when neither --changelog nor --github is given)")
	o.pvGithub = fs.Bool("github", false,
		"preview the GitHub release body, under the github entry format; beside --changelog, both are printed")
	o.computeWrite = fs.Bool("write", false,
		"apply every suggestion to the config file")
	o.computeInteractive = fs.BoolP("interactive", "i", false,
		"confirm each suggestion before applying it")
	o.check = fs.Bool("check", false,
		"report only, changing nothing, and exit 1 when there is something to do: for compute, config suggestions; for self-update, a release it would install; for download, a file the destination does not already hold (CI gate)")
	o.suRelease = fs.String("release", "",
		"self-update and download: install exactly this version instead of the latest one, downgrades included")
	o.suForce = fs.Bool("force", false,
		"self-update: install the selected release even when it is not newer, which repairs a damaged binary and leaves a prerelease line; download: install it even when the destination already carries it")
	o.suPrerelease = fs.Bool("prerelease", false,
		"self-update and download: consider prereleases too; ordering still decides, so a released 1.1.0 still wins over 1.1.0-rc.1")
	o.suRollback = fs.Bool("rollback", false,
		"self-update and download: put the binary the last install replaced back, without downloading anything")
	o.dlAsset = fs.String("asset", "",
		"download: which of the release's files to install, by name or glob; {os}, {arch}, {version}, {tag} and {name} are expanded (e.g. 'gh_{version}_{os}_{arch}.tar.gz'). A release carrying exactly one file needs none")
	o.dlBinDir = fs.String("bin-dir", "",
		"download: the folder to install into; without it $DISPAT_BIN_DIR, then /usr/local/bin when it is writable, then ~/.local/bin")
	o.dlName = fs.String("as", "",
		"download: what to call the installed tool; without it the repository's own name (--name is the commit committer)")
	o.dlPipe = fs.String("pipe", "",
		"download: hand the verified file to this command's standard input instead of installing it, run in --bin-dir, which is how an archive is unpacked ('tar -xz') or a release's install script run ('sh'); $DISPAT_ASSET names the same file by path")
	o.dlTagPrefix = fs.String("tag-prefix", "v",
		"download: what a release tag carries before its version; empty considers every tag whose whole name is a version")
	o.commitTag = fs.Bool("tag", false,
		"also create the annotated release tag at the resulting commit; an identical existing tag is skipped")
	o.commitPush = fs.Bool("push", false,
		"push the branch, and with --tag the tag(s)")
	o.commitNoForce = fs.Bool("no-force", false,
		"turn commit.force off: leave a tag the repository or the remote already carries as it is")
	o.commitName = fs.String("name", "",
		"override the commit.name committer identity")
	o.commitEmail = fs.String("email", "",
		"override the commit.email committer identity")
	o.commitRemote = fs.String("remote", "",
		"override the commit.remote push target")
	o.commitTagName = fs.String("tag-name", "",
		"name the annotated tag instead of computing it (pass $DISPAT_TAG from a release stage); one package only")
	o.commitMessage = fs.String("message-format", "",
		"override the commit.messageFormat template ({tags}, {packages})")
	o.commitInclude = fs.StringSlice("include", nil,
		"override the commit.include extra staged paths")
	o.ghOwner = fs.String("owner", "",
		"override the github.owner repository owner")
	o.ghRepo = fs.String("repo", "",
		"override the github.repo repository name")
	o.ghAPIURL = fs.String("api-url", "",
		"override the github.apiUrl API endpoint (for GitHub Enterprise)")
	o.ghTokenEnv = fs.String("token-env", "",
		"override the github.tokenEnv variable the token is read from")
	o.ghTarget = fs.String("target", "",
		"create the tag at this commit or branch (target_commitish); only safe once the commit is on the remote")
	o.clFile = fs.StringP("file", "f", "",
		"changelog: override the changelog.file name; if: the leading condition holds when this path exists and is a regular file")
	o.clFileTitle = fs.String("file-title", "",
		"override the changelog.fileTitle with this single line")
	o.clDateFormat = fs.String("date-format", "",
		"override the changelog.dateFormat entry date layout")
	o.releaseName = fs.String("release-name", "",
		"override the releaseName: the GitHub release's name, or the sub-header of a changelog entry ($VAR and ${VAR} are expanded)")
	o.authors = fs.String("authors", "",
		"override authors.placement, where the commit authors appear in the entry: off, inline (a \"(by ...)\" suffix per line), section (a list of its own) or both")
	o.authorsFormat = fs.String("authors-format", "",
		"override authors.format, how one author is written: fullname or username (the local part of the email)")
	o.authorsCommits = fs.String("authors-commits", "",
		"override authors.commits, which commits the authors section counts: ccme (the ones behind the entry's lines) or all (every commit in the window)")
	o.authorsInclude = fs.StringSlice("authors-include", nil,
		"override authors.include: only authors matching one of these case-insensitive globs are listed (matched against the full name, the username and the email)")
	o.authorsExclude = fs.StringSlice("authors-exclude", nil,
		"override authors.exclude: authors matching one of these globs are dropped, after --authors-include has been applied")
	o.authorsTitle = fs.String("authors-title", "",
		"override authors.title, the heading of the authors section")

	o.avRange = fs.String("range", "",
		"override the autoVersion.range write policy")
	o.avMatch = fs.StringSlice("match", nil,
		"override the autoVersion.match range globs")
	o.avManifests = fs.String("manifests", "",
		"which of a package's manifests are rewritten: root (the ones in the package folder), all (every manifest under it) or, for autoversion alone, none; empty takes autoVersion.manifests")
	o.onlyUpdated = fs.Bool("only-updated", false,
		"rewrite only the declarations naming a package this run updates, leaving a range that had fallen behind a provider released earlier as it is")
	o.avNoReplace = fs.Bool("no-replace", false,
		"skip the autoVersion.replace rules for this invocation")
	o.avWriteVersion = fs.Bool("write-version", true,
		"override autoVersion.writeVersion")
	o.avSyncLock = fs.Bool("sync-lock", true,
		"run the space's syncLock scripts for changed packages")
	o.ifThen = fs.StringArray("then", nil,
		"the script a condition runs when it holds; repeatable, paired in order with the leading condition and each --elif")
	o.ifElif = fs.StringArray("elif", nil,
		"another condition, tried when every earlier one was false; repeatable, each needing its own --then")
	o.ifElse = fs.String("else", "",
		"the script to run when no condition held; without it, nothing matching runs nothing and exits 0")
	o.ifChanged = fs.Bool("changed", false,
		"if: the leading condition holds when changed packages are selected (the release window, or what --since addresses), expanded downstream by --consumers and then narrowed by --package/--space/--group")
	o.ifDir = fs.StringP("dir", "d", "",
		"if: the leading condition holds when this path exists and is a folder; a relative path resolves where the chosen script runs, after --in")
	o.execFor = fs.String("for", "",
		"run the script of this level, in its environment: pkg:<name>, space:<name>, root or cwd (the package or space the invocation stands in); one exact name, no globs")
	o.execFallback = fs.Bool("fallback", false,
		"resolve the script name the way dispat run does, falling back from the package to its space to the top level, instead of the named level alone")
	o.execScriptFrom = fs.String("script-from", "",
		"take the script text from somewhere other than the subject: pkg:<name>, space:<name>, root or cwd; the environment still comes from the subject")
	o.execEnv = fs.String("env", app.EnvScopeStatic,
		"what the subject adds to the environment: static (its declared env), dispat (the DISPAT_* release variables, which computes a plan) or both")
	o.onFailure = fs.String("on-failure", "",
		"run this script when the chosen script fails, and exit with the failure script's code instead of the failed script's")
	o.helperIn = fs.String("in", "",
		"run the script in this folder: a path, or pkg:<name>, space:<name>, root or cwd; without it the script runs where the invocation stands (a folder actually called root or cwd is written ./root)")
	o.scanRootOnly = fs.Bool("root-only", false,
		"read only the manifests sitting directly in the folder, without descending")
	o.scVerifyUnlinked = fs.Bool("verify-unlinked", false,
		"scanner: fail when any manifest still carries a local-link directive, exactly what --link-local can inject (a go.mod replace, a Cargo patch, a uv source, a pubspec override, an npm file: override)")
	o.scVerifyLinked = fs.Bool("verify-linked", false,
		"scanner: fail when no manifest in the selection carries a local-link directive, proving a link step actually landed")
	o.scForbidRange = fs.StringArray("forbid-range", nil,
		"scanner: fail for every declared dependency range matching this pattern, literal text with * as a wildcard (repeatable)")
	o.scRequireRange = fs.StringArray("require-range", nil,
		"scanner: fail when no declared dependency range matches this pattern; each pattern is asked on its own (repeatable)")
	o.wrSetVersion = fs.String("set-version", "",
		"rewrite each manifest's own version field to this version")
	o.wrSetBuild = fs.String("set-build", "",
		"writer: write each manifest's build counter (CFBundleVersion, android:versionCode, CURRENT_PROJECT_VERSION, Gradle versionCode, a pubspec version's + suffix, Unity's bundle version codes, Godot's version/code, an Unreal plugin's Version and Android StoreVersion)")
	o.wrSet = fs.StringArray("set", nil,
		"set one dependency's declared range, [kind:]name=range (repeatable)")
	o.wrLink = fs.StringArray("link", nil,
		"point a dependency at a local folder, name=path; an empty path removes the redirect (repeatable)")
	o.wrSetLocal = fs.Bool("set-local", false,
		"set every declared workspace dependency to its provider's version, spelled by --range; no name has to be typed")
	o.wrLinkLocal = fs.Bool("link-local", false,
		"point every declared workspace dependency at the provider's folder; remove them again with --unlink-local before publishing")
	o.wrUnlinkLocal = fs.Bool("unlink-local", false,
		"remove the local folder redirect from every declared workspace dependency")
	o.wrDropLinks = fs.Bool("drop-links", false,
		"writer: remove every local-link directive the named manifests carry, no names needed")
	o.rpReplace = fs.StringArray("replace", nil,
		"replace literal text in the named files, find=>write (repeatable, applied in order)")
	o.rpFiles = fs.StringArray("files", nil,
		"autoreplacer: which files of each covered package to rewrite, as globs relative to its folder (repeatable)")
	o.strict = fs.Bool("strict", false,
		"turn a tolerated finding into a failure: for release and status, a selection the plan cannot release as it stands (a package waiting for its providers, a split versioning group), refused before anything is published; for scanner, a manifest that failed to parse; for writer, an edit the manifest does not declare; for replacer, a replacement that matched nothing; for autowriter, an edit that matched no manifest anywhere")
	o.requireRelease = fs.Bool("require-release", false,
		"release and status: exit 1 when the plan releases nothing, so a CI stage whose point is that this run publishes something fails instead of passing quietly (a held, withheld or unselected package does not count)")
	o.showVersion = fs.Bool("version", false, "print the dispat version and exit")
	// Declaring help is what makes it a flag rather than pflag's own
	// interception, which fires during Parse — before the command word has
	// been read — and is why help used to be the whole program's, whatever
	// command was asked for.
	o.showHelp = fs.BoolP("help", "h", false,
		"print help for the command and exit")
	return o
}

// authorOptions collects the six authors flags into the overlay the changelog
// and github step commands both take. They are one struct rather than six
// fields on each command's options because the two commands override exactly
// the same entry-format object, and splitting them would let the two drift.
func (o *options) authorOptions() app.AuthorOptions {
	return app.AuthorOptions{
		Placement: *o.authors,
		Format:    *o.authorsFormat,
		Commits:   *o.authorsCommits,
		Include:   *o.authorsInclude,
		Exclude:   *o.authorsExclude,
		Title:     *o.authorsTitle,
	}
}
